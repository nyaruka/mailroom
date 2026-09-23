package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

type KnowledgeSourceID int64

// NilKnowledgeSourceID is our constant for a nil knowledge id
const NilKnowledgeSourceID = KnowledgeSourceID(0)

func (i *KnowledgeSourceID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i KnowledgeSourceID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *KnowledgeSourceID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i KnowledgeSourceID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

type KnowledgeSourceUUID uuids.UUID

type KnowledgeSourceType string

const (
	KnowledgeSourceTypeShortcuts = KnowledgeSourceType("shortcuts") // the org's shortcuts, read from tickets_shortcut
	KnowledgeSourceTypeHelpdesk  = KnowledgeSourceType("helpdesk")  // the org's help articles, read from knowledge_article
	KnowledgeSourceTypeWebsite   = KnowledgeSourceType("website")   // a crawled website
	KnowledgeSourceTypeDocuments = KnowledgeSourceType("documents") // uploaded files
)

type KnowledgeSourceStatus string

const (
	KnowledgeSourceStatusPending  = KnowledgeSourceStatus("P") // needs (re)indexing, flagged by Django
	KnowledgeSourceStatusIndexing = KnowledgeSourceStatus("I") // being indexed
	KnowledgeSourceStatusReady    = KnowledgeSourceStatus("R") // indexed and searchable
	KnowledgeSourceStatusFailed   = KnowledgeSourceStatus("F") // last indexing attempt failed, see error
)

// KnowledgeSource is a source of knowledge that AI and human agents can search semantically. Django owns the schema and
// the CRUD but the status, error, counters and chunks are only ever written by mailroom as it indexes.
type KnowledgeSource struct {
	ID            KnowledgeSourceID     `db:"id"`
	UUID          KnowledgeSourceUUID   `db:"uuid"`
	OrgID         OrgID                 `db:"org_id"`
	Name          string                `db:"name"`
	Type          KnowledgeSourceType   `db:"source_type"`
	Config        JSONB[Config]         `db:"config"`
	Status        KnowledgeSourceStatus `db:"status"`
	Error         null.String           `db:"error"`
	LastIndexedOn *time.Time            `db:"last_indexed_on"`
	NumItems      int                   `db:"num_items"`
	NumChunks     int                   `db:"num_chunks"`
}

// A source is stale when it's active, of a type we can index, and either 1) flagged as pending by Django, 2) ready
// but an item in its Django owned table has been created, edited, unpublished or soft-deleted (all of which bump
// modified_on) since we last indexed it, 3) failed long enough ago to be worth retrying, or 4) stuck in indexing for
// over an hour - which can only mean the worker that started it died before recording an outcome, since starting
// bumps modified_on and there is no task retry.
//
// Indexing is normally triggered by Django as an edit commits, so in a healthy system this finds nothing. It exists
// because 'F' and 'I' would otherwise be dead ends: Django only moves a source to 'P' on the paths that own its
// config - uploading or deleting a document, editing a website - and there is no such path for the system sources,
// so nothing else would ever revive a failed shortcuts source and one embeddings outage would disable an org's
// knowledge permanently. Recovery from a failure deliberately goes through the timed branch alone rather than
// through the item-staleness branch above: starting an index bumps modified_on, so the interval is a real backoff,
// whereas a staleness-driven retry would re-queue on every sweep for as long as the underlying failure lasted.
const sqlSelectStaleKnowledgeSources = `
SELECT id, uuid, org_id, name, source_type, config, status, error, last_indexed_on, num_items, num_chunks
  FROM knowledge_knowledgesource k
 WHERE k.is_active AND k.source_type = ANY($1) AND (
         k.status = 'P'
      OR (k.status = 'R' AND k.source_type = 'shortcuts' AND EXISTS(
            SELECT 1 FROM tickets_shortcut s WHERE s.org_id = k.org_id AND s.modified_on > k.last_indexed_on))
      OR (k.status = 'R' AND k.source_type = 'helpdesk' AND EXISTS(
            SELECT 1 FROM knowledge_article a WHERE a.source_id = k.id AND a.modified_on > k.last_indexed_on))
      OR (k.status = 'F' AND k.modified_on < NOW() - INTERVAL '15 minutes')
      OR (k.status = 'I' AND k.modified_on < NOW() - INTERVAL '1 hour')
       )
 ORDER BY k.id
 LIMIT $2`

// GetStaleKnowledgeSources returns up to limit knowledge sources of the given types which need (re)indexing. Nothing is
// locked or updated here - the caller queues an indexing task per source and the task claims it.
func GetStaleKnowledgeSources(ctx context.Context, db *sqlx.DB, types []KnowledgeSourceType, limit int) ([]*KnowledgeSource, error) {
	rows, err := db.QueryxContext(ctx, sqlSelectStaleKnowledgeSources, StringArray(types), limit)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error querying stale knowledge sources: %w", err)
	}
	defer rows.Close()

	stale := make([]*KnowledgeSource, 0, 4)
	for rows.Next() {
		k := &KnowledgeSource{}
		if err := rows.StructScan(k); err != nil {
			return nil, fmt.Errorf("error unmarshalling knowledge source: %w", err)
		}
		stale = append(stale, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading knowledge sources: %w", err)
	}

	return stale, nil
}

const sqlSelectKnowledgeSource = `
SELECT id, uuid, org_id, name, source_type, config, status, error, last_indexed_on, num_items, num_chunks
  FROM knowledge_knowledgesource
 WHERE org_id = $1 AND uuid = $2 AND is_active`

// GetKnowledgeSource loads a knowledge source by UUID, returning nil if there's no such active source
func GetKnowledgeSource(ctx context.Context, db *sqlx.DB, orgID OrgID, uuid KnowledgeSourceUUID) (*KnowledgeSource, error) {
	k := &KnowledgeSource{}
	if err := db.GetContext(ctx, k, sqlSelectKnowledgeSource, orgID, uuid); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("error querying knowledge source: %w", err)
	}

	return k, nil
}

// is_active is guarded here for the same reason as in SetReady below - a source released since we picked up the
// work is on its way out and shouldn't be given a new status
const sqlSetKnowledgeIndexing = `
UPDATE knowledge_knowledgesource SET status = 'I', modified_on = NOW() WHERE id = $1 AND is_active`

// SetIndexing records that we've started indexing this source. Mutual exclusion between workers is the caller's
// lock, not this - the status is what the UI shows and what the retry cron reads.
func (k *KnowledgeSource) SetIndexing(ctx context.Context, db DBorTx) error {
	if _, err := db.ExecContext(ctx, sqlSetKnowledgeIndexing, k.ID); err != nil {
		return fmt.Errorf("error marking knowledge source as indexing: %w", err)
	}

	k.Status = KnowledgeSourceStatusIndexing
	return nil
}

// The is_active guard closes a race with release: Django can deactivate a source and purge its chunks while the
// worker that claimed it is still embedding. Without the guard that in-flight run would finalize the row back to 'R'
// with non-zero counters after the purge had emptied it.
const sqlSetKnowledgeReady = `
UPDATE knowledge_knowledgesource
   SET status = 'R', error = NULL, last_indexed_on = $2, num_items = $3, num_chunks = $4, modified_on = NOW()
 WHERE id = $1 AND is_active`

// ErrKnowledgeSourceReleased is returned when finalizing a source that was deactivated while we were indexing it
var ErrKnowledgeSourceReleased = errors.New("knowledge source is no longer active")

// SetReady records a successful indexing of this source. Returns ErrKnowledgeSourceReleased if the source was
// deactivated while we worked, so the caller can abandon the chunks it was about to write rather than repopulating a
// source Django has already purged.
func (k *KnowledgeSource) SetReady(ctx context.Context, db DBorTx, indexedOn time.Time, numItems, numChunks int) error {
	res, err := db.ExecContext(ctx, sqlSetKnowledgeReady, k.ID, indexedOn, numItems, numChunks)
	if err != nil {
		return fmt.Errorf("error marking knowledge source as ready: %w", err)
	}
	if rows, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("error checking rows affected: %w", err)
	} else if rows == 0 {
		return ErrKnowledgeSourceReleased
	}

	k.Status = KnowledgeSourceStatusReady
	k.Error = ""
	k.LastIndexedOn = &indexedOn
	k.NumItems = numItems
	k.NumChunks = numChunks
	return nil
}

const sqlSetKnowledgeFailed = `
UPDATE knowledge_knowledgesource SET status = 'F', error = $2, modified_on = NOW() WHERE id = $1`

// SetFailed records a failed indexing of this source
func (k *KnowledgeSource) SetFailed(ctx context.Context, db DBorTx, errMsg string) error {
	if runes := []rune(errMsg); len(runes) > 255 { // error column is varchar(255)
		errMsg = string(runes[:255])
	}

	if _, err := db.ExecContext(ctx, sqlSetKnowledgeFailed, k.ID, errMsg); err != nil {
		return fmt.Errorf("error marking knowledge source as failed: %w", err)
	}

	k.Status = KnowledgeSourceStatusFailed
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
	ID        KnowledgeChunkID  `db:"id"`
	SourceID  KnowledgeSourceID `db:"source_id"`
	ItemKey   uuids.UUID        `db:"item_key"`
	ItemName  string            `db:"item_name"`
	ItemURL   null.String       `db:"item_url"`
	Text      string            `db:"text"`
	Embedding Embedding         `db:"embedding"`
}

const sqlInsertKnowledgeChunk = `
INSERT INTO
  knowledge_knowledgechunk( source_id,  item_key,       item_name,  item_url,  text,  embedding)
                  VALUES(:source_id, :item_key::uuid, :item_name, :item_url, :text, :embedding::vector)`

// InsertKnowledgeChunks inserts the given chunks in batches of 100 - smaller than our usual 1000 because each row
// carries a 384 float embedding
func InsertKnowledgeChunks(ctx context.Context, tx DBorTx, chunks []*KnowledgeChunk) error {
	if err := BulkQueryBatches(ctx, "inserted knowledge chunks", tx, sqlInsertKnowledgeChunk, 100, chunks); err != nil {
		return fmt.Errorf("error inserting knowledge chunks: %w", err)
	}
	return nil
}

// DeleteKnowledgeChunks deletes the chunks of the given items of the given knowledge source
func DeleteKnowledgeChunks(ctx context.Context, tx DBorTx, sourceID KnowledgeSourceID, itemKeys []uuids.UUID) error {
	if len(itemKeys) == 0 {
		return nil
	}

	sql := `DELETE FROM knowledge_knowledgechunk WHERE source_id = $1 AND item_key = ANY($2)`
	if _, err := tx.ExecContext(ctx, sql, sourceID, pq.Array(itemKeys)); err != nil {
		return fmt.Errorf("error deleting knowledge chunks: %w", err)
	}
	return nil
}

// CountKnowledgeChunks returns the total number of chunks of the given knowledge source
func CountKnowledgeChunks(ctx context.Context, db DBorTx, sourceID KnowledgeSourceID) (int, error) {
	var count int
	if err := db.GetContext(ctx, &count, `SELECT count(*) FROM knowledge_knowledgechunk WHERE source_id = $1`, sourceID); err != nil {
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

type ArticleID int

// NilArticleID is our constant for a nil article id
const NilArticleID = ArticleID(0)

func (i *ArticleID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i ArticleID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *ArticleID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i ArticleID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

type ArticleStatus string

const (
	ArticleStatusDraft     = ArticleStatus("D") // never published, or pulled back
	ArticleStatusPublished = ArticleStatus("P")
)

// Article is an article in an org's helpdesk, owned entirely by Django - mailroom only reads them to index them. Like
// shortcuts they're soft-deleted, so a released article is a visible tombstone: it stays in the table with
// is_active = FALSE and a bumped modified_on. Unpublishing leaves the same kind of tombstone, just with a status of
// draft, which is why Indexable and not IsActive is what decides whether an article has content for us.
//
// A root of the tree - one with no parent - is a section: a heading over the articles filed under it, described in
// a line rather than written as an article.
type Article struct {
	ID         ArticleID         `db:"id"`
	UUID       uuids.UUID        `db:"uuid"`
	SourceID   KnowledgeSourceID `db:"source_id"`
	ParentID   ArticleID         `db:"parent_id"`
	Title      string            `db:"title"`
	Body       string            `db:"body"`
	Status     ArticleStatus     `db:"status"`
	IsActive   bool              `db:"is_active"`
	ModifiedOn time.Time         `db:"modified_on"`
}

// IsSection returns whether this is a section rather than an article - see Article
func (a *Article) IsSection() bool {
	return a.ParentID == NilArticleID
}

// Indexable is the single definition of which articles have content we're allowed to embed - and thus the only thing
// callers should ever branch on, and the same definition CountPublishedArticles counts by. Everything else is a
// tombstone which can only cause a chunk deletion: a draft has either never been published or has been pulled back,
// and in both cases its text must not be searchable, so a caller checking is_active alone would silently publish
// unreviewed writing into an org's knowledge; and a section is described rather than written, so whatever its body
// holds isn't content either.
func (a *Article) Indexable() bool {
	return a.IsActive && a.Status == ArticleStatusPublished && !a.IsSection()
}

const sqlSelectChangedArticles = `
SELECT id, uuid, source_id, parent_id, title, body, status, is_active, modified_on
  FROM knowledge_article
 WHERE source_id = $1 AND modified_on > $2
 ORDER BY modified_on`

// LoadChangedArticles loads the helpdesk's articles modified since the given time - creates, edits, unpublishes and
// soft-deletes alike since all of those bump modified_on. Scoped by source rather than by org because articles belong
// to a helpdesk, not to the org directly.
func LoadChangedArticles(ctx context.Context, db *sqlx.DB, sourceID KnowledgeSourceID, since time.Time) ([]*Article, error) {
	rows, err := db.QueryxContext(ctx, sqlSelectChangedArticles, sourceID, since)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error loading changed articles for knowledge source: %d: %w", sourceID, err)
	}
	defer rows.Close()

	articles := make([]*Article, 0, 10)
	for rows.Next() {
		a := &Article{}
		if err := rows.StructScan(a); err != nil {
			return nil, fmt.Errorf("error unmarshalling article: %w", err)
		}
		articles = append(articles, a)
	}
	// a truncated read here would be silently destructive: we'd index only what we managed to read, then advance
	// last_indexed_on past the modified_on of the ones we didn't, so their edits would never be picked up again
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading articles for knowledge source: %d: %w", sourceID, err)
	}

	return articles, nil
}

// CountPublishedArticles returns the number of indexable articles in the given helpdesk - active, published and not
// a section, exactly as Article.Indexable decides it - so that a helpdesk full of drafts doesn't report itself as
// indexed content.
func CountPublishedArticles(ctx context.Context, db DBorTx, sourceID KnowledgeSourceID) (int, error) {
	var count int
	sql := `SELECT count(*) FROM knowledge_article WHERE source_id = $1 AND parent_id IS NOT NULL AND is_active AND status = 'P'`
	if err := db.GetContext(ctx, &count, sql, sourceID); err != nil {
		return 0, fmt.Errorf("error counting published articles for knowledge source: %d: %w", sourceID, err)
	}
	return count, nil
}
