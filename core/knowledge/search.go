package knowledge

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
)

const (
	// DefaultSearchLimit is how many chunks a search returns when the caller doesn't say
	DefaultSearchLimit = 10

	// MaxSearchLimit caps it - strict_order iterative scans over the shared HNSW index get expensive with a large limit
	MaxSearchLimit = 100
)

// SearchResult is a chunk matching a knowledge search, scored by cosine similarity to the query (higher is better)
type SearchResult struct {
	KnowledgeUUID models.KnowledgeUUID `db:"knowledge_uuid" json:"knowledge_uuid"`
	ItemKey       uuids.UUID           `db:"item_key"       json:"item_key"`
	ItemName      string               `db:"item_name"      json:"item_name"`
	ItemURL       null.String          `db:"item_url"       json:"item_url,omitempty"`
	Text          string               `db:"text"           json:"text"`
	Score         float64              `db:"score"          json:"score"`
}

// A source that has completed an index before stays searchable whatever it's doing now. Chunks are only replaced
// inside a transaction that opens after embedding succeeds, so a source that is mid-reindex ('I') or whose last
// attempt failed ('F') is still serving its complete previous index - excluding either would blank an org's knowledge
// over a sweep, or over an entire embeddings outage plus its retry backoff, while perfectly good chunks sat there.
// Sources that have never completed an index have nothing to serve, so a NULL last_indexed_on is still excluded.
const sqlSearchKnowledgeChunks = `
  SELECT k.uuid AS knowledge_uuid, c.item_key, c.item_name, c.item_url, c.text, 1 - (c.embedding <=> $2::vector) AS score
    FROM knowledge_knowledgechunk c
    JOIN knowledge_knowledge k ON k.id = c.knowledge_id
   WHERE k.org_id = $1 AND k.is_active
     AND (k.status = 'R' OR (k.status IN ('I', 'F') AND k.last_indexed_on IS NOT NULL))
ORDER BY c.embedding <=> $2::vector
   LIMIT $3`

// Search performs a semantic search over the org's ready knowledge sources, returning the closest chunks by cosine
// distance. Returns no results if no embeddings service is configured since there can be nothing indexed.
func Search(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, query string, limit int) ([]*SearchResult, error) {
	// clamped here rather than only at the HTTP edge because this primitive is also called directly from Go, and will
	// eventually back an LLM tool where the limit can be model-influenced
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	limit = min(limit, MaxSearchLimit)

	results := make([]*SearchResult, 0, limit)

	if rt.Config.EmbeddingsEndpoint == "" {
		return results, nil
	}

	embedding, err := EmbedQuery(ctx, rt, query)
	if err != nil {
		return nil, fmt.Errorf("error embedding query: %w", err)
	}

	// reads go to the primary database because chunks are mutable and a search right after indexing should see them
	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error beginning transaction: %w", err)
	}

	// all orgs share one HNSW index, so an org filtered scan needs pgvector's iterative scans (>= 0.8) to guarantee
	// it can fill the limit
	if _, err := tx.ExecContext(ctx, `SET LOCAL hnsw.iterative_scan = 'strict_order'`); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error enabling iterative scans: %w", err)
	}

	rows, err := tx.QueryxContext(ctx, sqlSearchKnowledgeChunks, oa.OrgID(), embedding, limit)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error querying knowledge chunks: %w", err)
	}

	for rows.Next() {
		r := &SearchResult{}
		if err := rows.StructScan(r); err != nil {
			rows.Close()
			tx.Rollback()
			return nil, fmt.Errorf("error unmarshalling search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		tx.Rollback()
		return nil, fmt.Errorf("error reading knowledge chunks: %w", err)
	}
	rows.Close()

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error committing transaction: %w", err)
	}
	return results, nil
}
