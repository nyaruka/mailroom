package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

// IndexableTypes are the knowledge source types we can currently index
var IndexableTypes = []models.KnowledgeType{models.KnowledgeTypeShortcuts}

// how far back the last_indexed_on watermark is pulled to absorb skew between Django's clock and ours - see indexAuthored
const watermarkMargin = 30 * time.Second

// IndexSource indexes the given knowledge source - re-reading its changed content, chunking and embedding it, and
// replacing the affected chunks. On success the source is left ready with its counters updated. On error the caller
// is responsible for marking the source as failed - there is no task retry so that must always land in the database.
func IndexSource(ctx context.Context, rt *runtime.Runtime, k *models.Knowledge) error {
	switch k.Type {
	case models.KnowledgeTypeShortcuts:
		return indexAuthored(ctx, rt, k, changedShortcutItems, models.CountActiveShortcuts)
	default:
		return fmt.Errorf("unsupported knowledge type '%s'", k.Type)
	}
}

// an item of an authored source - content in its own Django owned table (shortcuts, later helpdesk articles) which
// mailroom reads but never writes. An inactive item is a tombstone: its chunks are deleted and nothing re-indexed.
type authoredItem struct {
	key    uuids.UUID
	name   string
	url    null.String
	text   string
	active bool
}

// indexAuthored indexes an authored source: a delta on modified_on since we last indexed catches creates, edits and
// soft-deletes alike because Django bumps modified_on for all three. A source never indexed (last_indexed_on null)
// deltas from the zero time, i.e. reads everything.
func indexAuthored(
	ctx context.Context,
	rt *runtime.Runtime,
	k *models.Knowledge,
	loadChanged func(context.Context, *sqlx.DB, models.OrgID, time.Time) ([]*authoredItem, error),
	countItems func(context.Context, models.DBorTx, models.OrgID) (int, error),
) error {
	// the new last_indexed_on watermark is taken before we read, so items changed while we index are picked up as
	// stale by a later sweep instead of being missed.
	//
	// The margin covers the two clocks involved: modified_on is stamped by Django before its transaction commits,
	// while this is mailroom's clock, so without it an item committing just after our read but stamped just before
	// it would land under the new watermark and never be seen as stale again. Re-reading an item we already indexed
	// is free - chunks are replaced by item_key - so erring earlier is strictly the safe direction.
	indexedOn := dates.Now().Add(-watermarkMargin)

	var since time.Time
	if k.LastIndexedOn != nil {
		since = *k.LastIndexedOn
	}

	changed, err := loadChanged(ctx, rt.DB, k.OrgID, since)
	if err != nil {
		return fmt.Errorf("error loading changed items: %w", err)
	}

	// chunk the still active items - inactive ones only contribute their key to the chunk deletion
	itemKeys := make([]uuids.UUID, len(changed))
	chunks := make([]*models.KnowledgeChunk, 0, len(changed))
	for i, item := range changed {
		itemKeys[i] = item.key
		if !item.active {
			continue
		}
		for _, text := range ChunkText(item.text) {
			chunks = append(chunks, &models.KnowledgeChunk{
				KnowledgeID: k.ID, ItemKey: item.key, ItemName: item.name, ItemURL: item.url, Text: text,
			})
		}
	}

	// embed all the new chunks (the client batches the requests)
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	embeddings, err := EmbedPassages(ctx, rt, texts)
	if err != nil {
		return fmt.Errorf("error embedding chunks: %w", err)
	}
	for i := range chunks {
		chunks[i].Embedding = embeddings[i]
	}

	// replace the changed items' chunks and finalize the counters in a single transaction so searches never see a
	// partially indexed item
	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	if err := models.DeleteKnowledgeChunks(ctx, tx, k.ID, itemKeys); err != nil {
		tx.Rollback()
		return err
	}
	if err := models.InsertKnowledgeChunks(ctx, tx, chunks); err != nil {
		tx.Rollback()
		return err
	}

	numItems, err := countItems(ctx, tx, k.OrgID)
	if err != nil {
		tx.Rollback()
		return err
	}
	numChunks, err := models.CountKnowledgeChunks(ctx, tx, k.ID)
	if err != nil {
		tx.Rollback()
		return err
	}

	if err := k.SetReady(ctx, tx, indexedOn, numItems, numChunks); err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return fmt.Errorf("error committing transaction: %w", err)
	}
	return nil
}

// loads the org's shortcuts changed since the given time as authored items keyed by the shortcut's UUID
func changedShortcutItems(ctx context.Context, db *sqlx.DB, orgID models.OrgID, since time.Time) ([]*authoredItem, error) {
	shortcuts, err := models.LoadChangedShortcuts(ctx, db, orgID, since)
	if err != nil {
		return nil, err
	}

	items := make([]*authoredItem, len(shortcuts))
	for i, s := range shortcuts {
		items[i] = &authoredItem{key: s.UUID, name: s.Name, text: s.Text, active: s.IsActive}
	}
	return items, nil
}
