package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

type KnowledgeID int64

// NilKnowledgeID is our constant for a nil knowledge id
const NilKnowledgeID = KnowledgeID(0)

func (i *KnowledgeID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i KnowledgeID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *KnowledgeID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i KnowledgeID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

type KnowledgeUUID uuids.UUID

type KnowledgeType string

const (
	KnowledgeTypeShortcuts = KnowledgeType("shortcuts") // the org's shortcuts, read straight from tickets_shortcut
	KnowledgeTypeHelpdesk  = KnowledgeType("helpdesk")  // the org's help articles, read from tickets_article
	KnowledgeTypeWebsite   = KnowledgeType("website")   // a crawled website
	KnowledgeTypeDocuments = KnowledgeType("documents") // uploaded files
)

type KnowledgeStatus string

const (
	KnowledgeStatusPending  = KnowledgeStatus("P") // needs (re)indexing, the sweep will pick it up
	KnowledgeStatusIndexing = KnowledgeStatus("I") // being indexed
	KnowledgeStatusReady    = KnowledgeStatus("R") // indexed and searchable
	KnowledgeStatusFailed   = KnowledgeStatus("F") // last indexing attempt failed, see error
)

// Knowledge is a source of knowledge that AI and human agents can search semantically. Django owns the schema and the
// CRUD but the status, error, counters and chunks are only ever written by mailroom as it indexes.
type Knowledge struct {
	ID            KnowledgeID     `db:"id"`
	UUID          KnowledgeUUID   `db:"uuid"`
	OrgID         OrgID           `db:"org_id"`
	Name          string          `db:"name"`
	Type          KnowledgeType   `db:"knowledge_type"`
	Config        JSONB[Config]   `db:"config"`
	Status        KnowledgeStatus `db:"status"`
	Error         null.String     `db:"error"`
	LastIndexedOn *time.Time      `db:"last_indexed_on"`
	NumItems      int             `db:"num_items"`
	NumChunks     int             `db:"num_chunks"`
}

// A source is stale and claimable when it's active, of a type we can index, and either 1) flagged as pending by
// Django, 2) ready but an item in its Django owned table has been created, edited or soft-deleted (all of which bump
// modified_on) since we last indexed it, 3) failed long enough ago to be worth retrying, or 4) stuck in indexing for
// over an hour - which can only mean the process that claimed it died before recording an outcome, since claiming
// bumps modified_on and there is no task retry.
//
// The retry branch is what keeps 'F' from being a dead end. Django only moves a source to 'P' on the paths that own
// its config - uploading or deleting a document, editing a website - and there is no such path for the system
// sources, so nothing outside this query would ever revive a failed shortcuts source and one embeddings outage would
// disable an org's knowledge permanently. Recovery deliberately goes through this timed branch alone rather than
// through the item-staleness branch above: claiming bumps modified_on, so the interval is a real backoff, whereas a
// staleness-driven retry would spin every sweep for as long as the underlying failure lasted.
// FOR UPDATE SKIP LOCKED lets concurrent sweeps claim disjoint sources without blocking on each other.
const sqlClaimStaleKnowledge = `
SELECT id, uuid, org_id, name, knowledge_type, config, status, error, last_indexed_on, num_items, num_chunks
  FROM tickets_knowledge k
 WHERE k.is_active AND k.knowledge_type = ANY($1) AND (
         k.status = 'P'
      OR (k.status = 'R' AND k.knowledge_type = 'shortcuts' AND EXISTS(
            SELECT 1 FROM tickets_shortcut s WHERE s.org_id = k.org_id AND s.modified_on > k.last_indexed_on))
      OR (k.status = 'F' AND k.modified_on < NOW() - INTERVAL '15 minutes')
      OR (k.status = 'I' AND k.modified_on < NOW() - INTERVAL '1 hour')
       )
 ORDER BY k.id
 LIMIT $2
   FOR UPDATE OF k SKIP LOCKED`

const sqlMarkKnowledgeIndexing = `
UPDATE tickets_knowledge SET status = 'I', modified_on = NOW() WHERE id = ANY($1)`

// ClaimStaleKnowledge locks up to limit stale knowledge sources of the given types and marks them as indexing. The
// claim is committed before returning so that the slow work of indexing doesn't hold row locks or a transaction open.
func ClaimStaleKnowledge(ctx context.Context, db *sqlx.DB, types []KnowledgeType, limit int) ([]*Knowledge, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error beginning transaction: %w", err)
	}

	rows, err := tx.QueryxContext(ctx, sqlClaimStaleKnowledge, StringArray(types), limit)
	if err != nil && err != sql.ErrNoRows {
		tx.Rollback()
		return nil, fmt.Errorf("error querying stale knowledge sources: %w", err)
	}

	claimed := make([]*Knowledge, 0, 4)
	for rows.Next() {
		k := &Knowledge{}
		if err := rows.StructScan(k); err != nil {
			rows.Close()
			tx.Rollback()
			return nil, fmt.Errorf("error unmarshalling knowledge source: %w", err)
		}
		claimed = append(claimed, k)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		tx.Rollback()
		return nil, fmt.Errorf("error reading knowledge sources: %w", err)
	}
	rows.Close()

	if len(claimed) > 0 {
		ids := make([]KnowledgeID, len(claimed))
		for i, k := range claimed {
			ids[i] = k.ID
		}
		if _, err := tx.ExecContext(ctx, sqlMarkKnowledgeIndexing, pq.Array(ids)); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("error marking knowledge sources as indexing: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error committing transaction: %w", err)
	}

	for _, k := range claimed {
		k.Status = KnowledgeStatusIndexing
	}
	return claimed, nil
}

// The is_active guard closes a race with release: Django can deactivate a source and purge its chunks while a sweep
// that already claimed it is still embedding. Without the guard that in-flight run would finalize the row back to 'R'
// with non-zero counters after the purge had emptied it.
const sqlSetKnowledgeReady = `
UPDATE tickets_knowledge
   SET status = 'R', error = NULL, last_indexed_on = $2, num_items = $3, num_chunks = $4, modified_on = NOW()
 WHERE id = $1 AND is_active`

// SetReady records a successful indexing of this source
func (k *Knowledge) SetReady(ctx context.Context, db DBorTx, indexedOn time.Time, numItems, numChunks int) error {
	if _, err := db.ExecContext(ctx, sqlSetKnowledgeReady, k.ID, indexedOn, numItems, numChunks); err != nil {
		return fmt.Errorf("error marking knowledge source as ready: %w", err)
	}

	k.Status = KnowledgeStatusReady
	k.Error = ""
	k.LastIndexedOn = &indexedOn
	k.NumItems = numItems
	k.NumChunks = numChunks
	return nil
}

const sqlSetKnowledgeFailed = `
UPDATE tickets_knowledge SET status = 'F', error = $2, modified_on = NOW() WHERE id = $1`

// SetFailed records a failed indexing of this source
func (k *Knowledge) SetFailed(ctx context.Context, db DBorTx, errMsg string) error {
	if runes := []rune(errMsg); len(runes) > 255 { // error column is varchar(255)
		errMsg = string(runes[:255])
	}

	if _, err := db.ExecContext(ctx, sqlSetKnowledgeFailed, k.ID, errMsg); err != nil {
		return fmt.Errorf("error marking knowledge source as failed: %w", err)
	}

	k.Status = KnowledgeStatusFailed
	k.Error = null.String(errMsg)
	return nil
}

type KnowledgeChunkID int64

// NilKnowledgeChunkID is our constant for a nil knowledge chunk id
const NilKnowledgeChunkID = KnowledgeChunkID(0)

func (i *KnowledgeChunkID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i KnowledgeChunkID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *KnowledgeChunkID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i KnowledgeChunkID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

// KnowledgeChunk is an embedded chunk of a knowledge source's content. Its item_key is the UUID of the item it came
// from - for shortcuts that's tickets_shortcut.uuid - letting us replace an item's chunks without per-item state.
type KnowledgeChunk struct {
	ID          KnowledgeChunkID `db:"id"`
	KnowledgeID KnowledgeID      `db:"knowledge_id"`
	ItemKey     uuids.UUID       `db:"item_key"`
	ItemName    string           `db:"item_name"`
	ItemURL     null.String      `db:"item_url"`
	Text        string           `db:"text"`
	Embedding   Embedding        `db:"embedding"`
}

const sqlInsertKnowledgeChunk = `
INSERT INTO
  tickets_knowledgechunk( knowledge_id,  item_key,       item_name,  item_url,  text,  embedding)
                  VALUES(:knowledge_id, :item_key::uuid, :item_name, :item_url, :text, :embedding::vector)`

// InsertKnowledgeChunks inserts the given chunks in batches of 100 - smaller than our usual 1000 because each row
// carries a 384 float embedding
func InsertKnowledgeChunks(ctx context.Context, tx DBorTx, chunks []*KnowledgeChunk) error {
	if err := BulkQueryBatches(ctx, "inserted knowledge chunks", tx, sqlInsertKnowledgeChunk, 100, chunks); err != nil {
		return fmt.Errorf("error inserting knowledge chunks: %w", err)
	}
	return nil
}

// DeleteKnowledgeChunks deletes the chunks of the given items of the given knowledge source
func DeleteKnowledgeChunks(ctx context.Context, tx DBorTx, knowledgeID KnowledgeID, itemKeys []uuids.UUID) error {
	if len(itemKeys) == 0 {
		return nil
	}

	sql := `DELETE FROM tickets_knowledgechunk WHERE knowledge_id = $1 AND item_key = ANY($2)`
	if _, err := tx.ExecContext(ctx, sql, knowledgeID, pq.Array(itemKeys)); err != nil {
		return fmt.Errorf("error deleting knowledge chunks: %w", err)
	}
	return nil
}

// CountKnowledgeChunks returns the total number of chunks of the given knowledge source
func CountKnowledgeChunks(ctx context.Context, db DBorTx, knowledgeID KnowledgeID) (int, error) {
	var count int
	if err := db.GetContext(ctx, &count, `SELECT count(*) FROM tickets_knowledgechunk WHERE knowledge_id = $1`, knowledgeID); err != nil {
		return 0, fmt.Errorf("error counting knowledge chunks: %w", err)
	}
	return count, nil
}

type ShortcutID int

// NilShortcutID is our constant for a nil shortcut id
const NilShortcutID = ShortcutID(0)

func (i *ShortcutID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i ShortcutID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *ShortcutID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i ShortcutID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

// Shortcut is a canned ticket response, owned entirely by Django - mailroom only reads them to index them. A released
// shortcut is a visible tombstone: it stays in the table with is_active = FALSE and a bumped modified_on.
type Shortcut struct {
	ID         ShortcutID `db:"id"`
	UUID       uuids.UUID `db:"uuid"`
	OrgID      OrgID      `db:"org_id"`
	Name       string     `db:"name"`
	Text       string     `db:"text"`
	IsActive   bool       `db:"is_active"`
	ModifiedOn time.Time  `db:"modified_on"`
}

const sqlSelectChangedShortcuts = `
SELECT id, uuid, org_id, name, text, is_active, modified_on
  FROM tickets_shortcut
 WHERE org_id = $1 AND modified_on > $2
 ORDER BY modified_on`

// LoadChangedShortcuts loads the org's shortcuts modified since the given time - creates, edits and soft-deletes alike
// since releasing a shortcut bumps its modified_on
func LoadChangedShortcuts(ctx context.Context, db *sqlx.DB, orgID OrgID, since time.Time) ([]*Shortcut, error) {
	rows, err := db.QueryxContext(ctx, sqlSelectChangedShortcuts, orgID, since)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error loading changed shortcuts for org: %d: %w", orgID, err)
	}
	defer rows.Close()

	shortcuts := make([]*Shortcut, 0, 10)
	for rows.Next() {
		s := &Shortcut{}
		if err := rows.StructScan(s); err != nil {
			return nil, fmt.Errorf("error unmarshalling shortcut: %w", err)
		}
		shortcuts = append(shortcuts, s)
	}
	// a truncated read here would be silently destructive: we'd index only what we managed to read, then advance
	// last_indexed_on past the modified_on of the ones we didn't, so their edits would never be picked up again
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading shortcuts for org: %d: %w", orgID, err)
	}

	return shortcuts, nil
}

// CountActiveShortcuts returns the number of active shortcuts in the given org
func CountActiveShortcuts(ctx context.Context, db DBorTx, orgID OrgID) (int, error) {
	var count int
	if err := db.GetContext(ctx, &count, `SELECT count(*) FROM tickets_shortcut WHERE org_id = $1 AND is_active`, orgID); err != nil {
		return 0, fmt.Errorf("error counting active shortcuts for org: %d: %w", orgID, err)
	}
	return count, nil
}
