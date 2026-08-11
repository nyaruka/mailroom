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
	KnowledgeTypeHelpdesk  = KnowledgeType("helpdesk")  // the org's help articles, read from knowledge_article
	KnowledgeTypeWebsite   = KnowledgeType("website")   // a crawled website
	KnowledgeTypeDocuments = KnowledgeType("documents") // uploaded files
)

type KnowledgeStatus string

const (
	KnowledgeStatusPending  = KnowledgeStatus("P") // needs (re)indexing, flagged by Django
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

// A source is stale when it's active, of a type we can index, and either 1) flagged as pending by Django, 2) ready
// but an item in its Django owned table has been created, edited or soft-deleted (all of which bump modified_on)
// since we last indexed it, 3) failed long enough ago to be worth retrying, or 4) stuck in indexing for over an hour
// - which can only mean the process that claimed it died before recording an outcome, since claiming bumps
// modified_on and there is no task retry.
//
// Indexing is normally triggered by Django as an edit commits, so in a healthy system this finds nothing. It exists
// because 'F' and 'I' would otherwise be dead ends: Django only moves a source to 'P' on the paths that own its
// config - uploading or deleting a document, editing a website - and there is no such path for the system sources,
// so nothing else would ever revive a failed shortcuts source and one embeddings outage would disable an org's
// knowledge permanently. Recovery from a failure deliberately goes through the timed branch alone rather than
// through the item-staleness branch above: claiming bumps modified_on, so the interval is a real backoff, whereas a
// staleness-driven retry would re-queue on every sweep for as long as the underlying failure lasted.
const sqlSelectStaleKnowledge = `
SELECT id, uuid, org_id, name, knowledge_type, config, status, error, last_indexed_on, num_items, num_chunks
  FROM knowledge_knowledge k
 WHERE k.is_active AND k.knowledge_type = ANY($1) AND (
         k.status = 'P'
      OR (k.status = 'R' AND k.knowledge_type = 'shortcuts' AND EXISTS(
            SELECT 1 FROM tickets_shortcut s WHERE s.org_id = k.org_id AND s.modified_on > k.last_indexed_on))
      OR (k.status = 'F' AND k.modified_on < NOW() - INTERVAL '15 minutes')
      OR (k.status = 'I' AND k.modified_on < NOW() - INTERVAL '1 hour')
       )
 ORDER BY k.id
 LIMIT $2`

// GetStaleKnowledge returns up to limit knowledge sources of the given types which need (re)indexing. Nothing is
// locked or updated here - the caller queues an indexing task per source and the task claims it.
func GetStaleKnowledge(ctx context.Context, db *sqlx.DB, types []KnowledgeType, limit int) ([]*Knowledge, error) {
	rows, err := db.QueryxContext(ctx, sqlSelectStaleKnowledge, StringArray(types), limit)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error querying stale knowledge sources: %w", err)
	}
	defer rows.Close()

	stale := make([]*Knowledge, 0, 4)
	for rows.Next() {
		k := &Knowledge{}
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

// A source is claimable unless it's already being indexed - so triggers arriving for the same source while a worker
// is working on it collapse into that one run. The exception is a source left in 'I' for longer than any index can
// legitimately take, which means the worker that claimed it died without recording an outcome.
//
// FOR UPDATE SKIP LOCKED means two workers racing to claim the same source don't queue up behind each other: the
// loser sees no row and no-ops, rather than blocking and then claiming a source that's just been indexed.
const sqlClaimKnowledge = `
SELECT id, uuid, org_id, name, knowledge_type, config, status, error, last_indexed_on, num_items, num_chunks
  FROM knowledge_knowledge k
 WHERE k.org_id = $1 AND k.uuid = $2 AND k.is_active AND k.knowledge_type = ANY($3)
   AND (k.status != 'I' OR k.modified_on < NOW() - INTERVAL '1 hour')
   FOR UPDATE OF k SKIP LOCKED`

const sqlMarkKnowledgeIndexing = `
UPDATE knowledge_knowledge SET status = 'I', modified_on = NOW() WHERE id = $1`

// ClaimKnowledge locks the given knowledge source and marks it as indexing, returning nil if it isn't there, isn't
// of a type we can index, has been released, or is already being indexed by someone else. The claim is committed
// before returning so that the slow work of indexing doesn't hold a row lock or a transaction open.
func ClaimKnowledge(ctx context.Context, db *sqlx.DB, orgID OrgID, uuid KnowledgeUUID, types []KnowledgeType) (*Knowledge, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error beginning transaction: %w", err)
	}

	k := &Knowledge{}
	if err := tx.GetContext(ctx, k, sqlClaimKnowledge, orgID, uuid, StringArray(types)); err != nil {
		tx.Rollback()

		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("error querying knowledge source: %w", err)
	}

	if _, err := tx.ExecContext(ctx, sqlMarkKnowledgeIndexing, k.ID); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error marking knowledge source as indexing: %w", err)
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error committing transaction: %w", err)
	}

	k.Status = KnowledgeStatusIndexing
	return k, nil
}

// The is_active guard closes a race with release: Django can deactivate a source and purge its chunks while the
// worker that claimed it is still embedding. Without the guard that in-flight run would finalize the row back to 'R'
// with non-zero counters after the purge had emptied it.
const sqlSetKnowledgeReady = `
UPDATE knowledge_knowledge
   SET status = 'R', error = NULL, last_indexed_on = $2, num_items = $3, num_chunks = $4, modified_on = NOW()
 WHERE id = $1 AND is_active`

// ErrKnowledgeReleased is returned when finalizing a source that was deactivated while we were indexing it
var ErrKnowledgeReleased = errors.New("knowledge source is no longer active")

// SetReady records a successful indexing of this source. Returns ErrKnowledgeReleased if the source was deactivated
// while we worked, so the caller can abandon the chunks it was about to write rather than repopulating a source
// Django has already purged.
func (k *Knowledge) SetReady(ctx context.Context, db DBorTx, indexedOn time.Time, numItems, numChunks int) error {
	res, err := db.ExecContext(ctx, sqlSetKnowledgeReady, k.ID, indexedOn, numItems, numChunks)
	if err != nil {
		return fmt.Errorf("error marking knowledge source as ready: %w", err)
	}
	if rows, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("error checking rows affected: %w", err)
	} else if rows == 0 {
		return ErrKnowledgeReleased
	}

	k.Status = KnowledgeStatusReady
	k.Error = ""
	k.LastIndexedOn = &indexedOn
	k.NumItems = numItems
	k.NumChunks = numChunks
	return nil
}

const sqlSetKnowledgeFailed = `
UPDATE knowledge_knowledge SET status = 'F', error = $2, modified_on = NOW() WHERE id = $1`

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
  knowledge_knowledgechunk( knowledge_id,  item_key,       item_name,  item_url,  text,  embedding)
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

	sql := `DELETE FROM knowledge_knowledgechunk WHERE knowledge_id = $1 AND item_key = ANY($2)`
	if _, err := tx.ExecContext(ctx, sql, knowledgeID, pq.Array(itemKeys)); err != nil {
		return fmt.Errorf("error deleting knowledge chunks: %w", err)
	}
	return nil
}

// CountKnowledgeChunks returns the total number of chunks of the given knowledge source
func CountKnowledgeChunks(ctx context.Context, db DBorTx, knowledgeID KnowledgeID) (int, error) {
	var count int
	if err := db.GetContext(ctx, &count, `SELECT count(*) FROM knowledge_knowledgechunk WHERE knowledge_id = $1`, knowledgeID); err != nil {
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
