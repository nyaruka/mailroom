package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

// IndexableTypes are the knowledge source types we can currently index
var IndexableTypes = []models.KnowledgeType{models.KnowledgeTypeShortcuts, models.KnowledgeTypeHelpdesk}

// how far back the last_indexed_on watermark is pulled - see indexAuthored. Sized to cover clock skew between the
// hosts plus the length of a Django write transaction, and no more: anything modified inside the margin is read
// again by the next run for this source, and re-reading means re-embedding.
const watermarkMargin = 5 * time.Second

// IndexSource indexes the given knowledge source - re-reading its changed content, chunking and embedding it, and
// replacing the affected chunks. On success the source is left ready with its counters updated. On error the caller
// is responsible for marking the source as failed - there is no task retry so that must always land in the database.
func IndexSource(ctx context.Context, rt *runtime.Runtime, k *models.Knowledge) error {
	switch k.Type {
	case models.KnowledgeTypeShortcuts:
		return indexAuthored(ctx, rt, k, shortcutsSource)
	case models.KnowledgeTypeHelpdesk:
		return indexAuthored(ctx, rt, k, helpdeskSource)
	default:
		return fmt.Errorf("unsupported knowledge type '%s'", k.Type)
	}
}

// an item of an authored source - content in its own Django owned table (shortcuts, helpdesk articles) which mailroom
// reads but never writes. An item that isn't indexable is a tombstone: its chunks are deleted and nothing re-indexed.
type authoredItem struct {
	key       uuids.UUID
	name      string
	url       null.String
	text      string
	indexable bool
}

// how to read and chunk one kind of authored source. Everything type specific lives here so that indexAuthored itself
// - which is where the watermark and the replace-and-finalize transaction live - stays the same for every source.
type authoredSource struct {
	loadChanged func(context.Context, *sqlx.DB, *models.Knowledge, time.Time) ([]*authoredItem, error)
	countItems  func(context.Context, models.DBorTx, *models.Knowledge) (int, error)
	chunkItem   func(*authoredItem) []string
}

var (
	shortcutsSource = &authoredSource{loadChanged: changedShortcutItems, countItems: countShortcutItems, chunkItem: chunkShortcut}
	helpdeskSource  = &authoredSource{loadChanged: changedArticleItems, countItems: countArticleItems, chunkItem: chunkArticle}
)

// indexAuthored indexes an authored source: a delta on modified_on since we last indexed catches creates, edits and
// soft-deletes alike because Django bumps modified_on for all three. A source never indexed (last_indexed_on null)
// deltas from the zero time, i.e. reads everything.
func indexAuthored(ctx context.Context, rt *runtime.Runtime, k *models.Knowledge, src *authoredSource) error {
	// the new last_indexed_on watermark is taken before we read, so items changed while we index leave the source
	// stale for the next trigger or the retry cron to pick up instead of being missed.
	//
	// The margin covers the two clocks involved: modified_on is stamped by Django before its transaction commits,
	// while this is mailroom's clock, so without it an item committing just after our read but stamped just before
	// it would land under the new watermark and never be seen as stale again - a silently missed edit that only a
	// later edit of the same item would heal. Re-reading an item we already indexed is harmless since chunks are
	// replaced by item_key, so erring earlier is the safe direction - but it isn't free (it re-embeds), which is
	// why the margin is no bigger than it needs to be.
	indexedOn := dates.Now().Add(-watermarkMargin)

	var since time.Time
	if k.LastIndexedOn != nil {
		since = *k.LastIndexedOn
	}

	changed, err := src.loadChanged(ctx, rt.DB, k, since)
	if err != nil {
		return fmt.Errorf("error loading changed items: %w", err)
	}

	// chunk the still indexable items - the rest only contribute their key to the chunk deletion
	itemKeys := make([]uuids.UUID, len(changed))
	chunks := make([]*models.KnowledgeChunk, 0, len(changed))
	for i, item := range changed {
		itemKeys[i] = item.key
		if !item.indexable {
			continue
		}
		for _, text := range src.chunkItem(item) {
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
	embeddings, err := rt.Embeddings.EmbedPassages(ctx, texts)
	if err != nil {
		return fmt.Errorf("error embedding chunks: %w", err)
	}
	for i := range chunks {
		chunks[i].Embedding = models.Embedding(embeddings[i])
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

	numItems, err := src.countItems(ctx, tx, k)
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

		// released mid-index isn't a failure - Django has purged this source and we simply throw away the chunks we
		// were about to write for it. Marking it failed would resurrect a row that is on its way out.
		if errors.Is(err, models.ErrKnowledgeReleased) {
			slog.Info("knowledge source released while indexing, discarding", "knowledge_id", k.ID)
			return nil
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return fmt.Errorf("error committing transaction: %w", err)
	}
	return nil
}

// loads the org's shortcuts changed since the given time as authored items keyed by the shortcut's UUID
func changedShortcutItems(ctx context.Context, db *sqlx.DB, k *models.Knowledge, since time.Time) ([]*authoredItem, error) {
	shortcuts, err := models.LoadChangedShortcuts(ctx, db, k.OrgID, since)
	if err != nil {
		return nil, err
	}

	items := make([]*authoredItem, len(shortcuts))
	for i, s := range shortcuts {
		items[i] = &authoredItem{key: s.UUID, name: s.Name, text: s.Text, indexable: s.IsActive}
	}
	return items, nil
}

func countShortcutItems(ctx context.Context, db models.DBorTx, k *models.Knowledge) (int, error) {
	return models.CountActiveShortcuts(ctx, db, k.OrgID)
}

// a shortcut's text is plain prose written to be sent as-is, so it chunks as plain text
func chunkShortcut(item *authoredItem) []string {
	return ChunkText(item.text)
}

// loads the helpdesk's articles changed since the given time as authored items keyed by the article's UUID. Only
// published, active articles are indexable - see models.Article.Indexable - so an unpublish reaches us as a tombstone
// exactly like a delete does.
//
// ItemURL is deliberately left null even though the helpdesk may have a public site: an article's address there is
// its section's slug, its own slug and the site's domain, and a section rename or a domain change alters it without
// bumping the article's modified_on - so a URL baked into a chunk would go stale with no way of noticing. Whoever
// shows a hit resolves the article by its key instead, which is always right as of that moment.
func changedArticleItems(ctx context.Context, db *sqlx.DB, k *models.Knowledge, since time.Time) ([]*authoredItem, error) {
	articles, err := models.LoadChangedArticles(ctx, db, k.ID, since)
	if err != nil {
		return nil, err
	}

	items := make([]*authoredItem, len(articles))
	for i, a := range articles {
		items[i] = &authoredItem{key: a.UUID, name: a.Title, text: a.Body, indexable: a.Indexable()}
	}
	return items, nil
}

func countArticleItems(ctx context.Context, db models.DBorTx, k *models.Knowledge) (int, error) {
	return models.CountPublishedArticles(ctx, db, k.ID)
}

// an article body is authored markdown, so it chunks on its headings. Every chunk is then prefixed with the article's
// title rather than just the first one: a chunk from halfway down a long article is otherwise anonymous both to the
// embedding and to whoever reads a citation of it, and a title costs little next to a chunk of a thousand runes.
// Shortcuts deliberately don't do this - a shortcut is short enough to be one chunk and its name is a filing label
// ("Greeting"), not a subject the text is about.
func chunkArticle(item *authoredItem) []string {
	chunks := ChunkMarkdown(item.text)
	for i := range chunks {
		chunks[i] = item.name + "\n\n" + chunks[i]
	}
	return chunks
}
