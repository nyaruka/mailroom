package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// creates a 384 dimension embedding with the given components set from vals
func testEmbedding(vals ...float32) models.Embedding {
	e := make(models.Embedding, 384)
	copy(e, vals)
	return e
}

func TestGetStaleKnowledgeSources(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// deactivate the system sources baked into the test database so only our test sources are seen
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE`)

	// pending source.. stale
	k1 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeSourceTypeShortcuts, "Pending", models.KnowledgeSourceStatusPending)

	// pending but released source.. not stale
	k2 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeSourceTypeShortcuts, "Released", models.KnowledgeSourceStatusPending)
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE WHERE id = $1`, k2.ID)

	// ready source whose org has a shortcut modified since it was indexed.. not stale, Django flags a source pending
	// with any change to its items, so the sweep never has to look for them
	k3 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", models.KnowledgeSourceTypeShortcuts, "Ready", models.KnowledgeSourceStatusReady)
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET last_indexed_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, k3.ID)

	testdb.InsertShortcut(t, rt, testdb.Org1, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", "Refunds", "We offer full refunds within 30 days.")

	// source that failed recently.. not stale yet, the retry interval is the backoff
	testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "9f0b4b7c-3a17-4f5e-95a8-4d68f21e2a7d", models.KnowledgeSourceTypeShortcuts, "Failed", models.KnowledgeSourceStatusFailed)

	// source that failed long enough ago.. stale again, since otherwise nothing would re-pend it until its next edit
	k9 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "3c5f1e0e-2b4a-4f9c-8d1e-7a6b5c4d3e2f", models.KnowledgeSourceTypeShortcuts, "FailedOld", models.KnowledgeSourceStatusFailed)
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET modified_on = NOW() - INTERVAL '30 minutes' WHERE id = $1`, k9.ID)

	// source currently being indexed by a worker.. not stale
	testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "0a1c6a9a-52ed-40cb-a921-1a29b9d8bc6f", models.KnowledgeSourceTypeShortcuts, "Indexing", models.KnowledgeSourceStatusIndexing)

	// source stuck in indexing for over an hour.. stale, the worker that started it can only have died
	k7 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "639a26a3-8e6e-4ad0-b4b9-6bf7c1cf42d1", models.KnowledgeSourceTypeShortcuts, "Stuck", models.KnowledgeSourceStatusIndexing)
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET modified_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, k7.ID)

	// pending source of a type we don't support.. not stale
	testdb.InsertKnowledgeSource(t, rt, testdb.Org2, "df22cbcb-e0e1-4e78-be9f-2e4fbea1b2c3", models.KnowledgeSourceTypeWebsite, "Website", models.KnowledgeSourceStatusPending)

	// pending helpdesk.. stale
	k10 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "6d4c2b1a-0f9e-4c8d-b7a6-5e4f3d2c1b0a", models.KnowledgeSourceTypeHelpdesk, "Pending Helpdesk", models.KnowledgeSourceStatusPending)

	types := []models.KnowledgeSourceType{models.KnowledgeSourceTypeShortcuts, models.KnowledgeSourceTypeHelpdesk}

	stale, err := models.GetStaleKnowledgeSources(ctx, rt.DB, types, 10)
	require.NoError(t, err)
	require.Len(t, stale, 4)
	assert.Equal(t, k1.ID, stale[0].ID)
	assert.Equal(t, k1.UUID, stale[0].UUID)
	assert.Equal(t, testdb.Org1.ID, stale[0].OrgID)
	assert.Equal(t, models.KnowledgeSourceTypeShortcuts, stale[0].Type)
	assert.Equal(t, models.KnowledgeSourceStatusPending, stale[0].Status)
	assert.Equal(t, k9.ID, stale[1].ID)
	assert.Equal(t, k7.ID, stale[2].ID)
	assert.Equal(t, k10.ID, stale[3].ID)
	assert.Equal(t, models.KnowledgeSourceTypeHelpdesk, stale[3].Type)

	// asking only for shortcuts leaves the helpdesk out
	stale, err = models.GetStaleKnowledgeSources(ctx, rt.DB, []models.KnowledgeSourceType{models.KnowledgeSourceTypeShortcuts}, 10)
	require.NoError(t, err)
	require.Len(t, stale, 3)

	// the limit is honoured, taking the lowest ids
	stale, err = models.GetStaleKnowledgeSources(ctx, rt.DB, types, 2)
	require.NoError(t, err)
	require.Len(t, stale, 2)
	assert.Equal(t, k1.ID, stale[0].ID)
	assert.Equal(t, k9.ID, stale[1].ID)

	// and nothing was claimed or otherwise modified by looking
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgesource WHERE status = 'I'`).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledgesource WHERE id = $1`, k1.ID).Returns("P")
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledgesource WHERE id = $1`, k3.ID).Returns("R")
}

func TestGetKnowledgeSource(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE`)

	k1 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeSourceTypeShortcuts, "Test Shortcuts", models.KnowledgeSourceStatusPending)

	k, err := models.GetKnowledgeSource(ctx, rt.DB, testdb.Org1.ID, k1.UUID)
	require.NoError(t, err)
	require.NotNil(t, k)
	assert.Equal(t, k1.ID, k.ID)
	assert.Equal(t, k1.UUID, k.UUID)
	assert.Equal(t, testdb.Org1.ID, k.OrgID)
	assert.Equal(t, "Test Shortcuts", k.Name)
	assert.Equal(t, models.KnowledgeSourceTypeShortcuts, k.Type)
	assert.Equal(t, models.KnowledgeSourceStatusPending, k.Status)

	// a released source is as good as gone
	k2 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeSourceTypeShortcuts, "Released", models.KnowledgeSourceStatusPending)
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE WHERE id = $1`, k2.ID)

	k, err = models.GetKnowledgeSource(ctx, rt.DB, testdb.Org1.ID, k2.UUID)
	require.NoError(t, err)
	assert.Nil(t, k)

	// as is one belonging to a different org
	k3 := testdb.InsertKnowledgeSource(t, rt, testdb.Org2, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", models.KnowledgeSourceTypeShortcuts, "Other Org", models.KnowledgeSourceStatusPending)

	k, err = models.GetKnowledgeSource(ctx, rt.DB, testdb.Org1.ID, k3.UUID)
	require.NoError(t, err)
	assert.Nil(t, k)

	// and one that doesn't exist at all isn't an error
	k, err = models.GetKnowledgeSource(ctx, rt.DB, testdb.Org1.ID, "9f0b4b7c-3a17-4f5e-95a8-4d68f21e2a7d")
	require.NoError(t, err)
	assert.Nil(t, k)
}

func TestSetIndexing(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE`)

	k1 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeSourceTypeShortcuts, "Test Shortcuts", models.KnowledgeSourceStatusPending)

	k, err := models.GetKnowledgeSource(ctx, rt.DB, testdb.Org1.ID, k1.UUID)
	require.NoError(t, err)

	err = k.SetIndexing(ctx, rt.DB)
	assert.NoError(t, err)
	assert.Equal(t, models.KnowledgeSourceStatusIndexing, k.Status)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledgesource WHERE id = $1`, k1.ID).Returns("I")

	// a source released since we picked up the work isn't given a new status
	k2 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeSourceTypeShortcuts, "Released", models.KnowledgeSourceStatusPending)
	released := &models.KnowledgeSource{ID: k2.ID}
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE WHERE id = $1`, k2.ID)

	err = released.SetIndexing(ctx, rt.DB)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledgesource WHERE id = $1`, k2.ID).Returns("P")
}

func TestSetReadyAndSetFailed(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE`)

	k1 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeSourceTypeShortcuts, "Test", models.KnowledgeSourceStatusPending)

	k, err := models.GetKnowledgeSource(ctx, rt.DB, testdb.Org1.ID, k1.UUID)
	require.NoError(t, err)
	require.NotNil(t, k)

	require.NoError(t, k.SetIndexing(ctx, rt.DB))

	indexedOn := dates.Now().In(time.UTC).Truncate(time.Millisecond)
	err = k.SetReady(ctx, rt.DB, indexedOn, 3, 7)
	assert.NoError(t, err)
	assert.Equal(t, models.KnowledgeSourceStatusReady, k.Status)
	assert.Equal(t, 3, k.NumItems)
	assert.Equal(t, 7, k.NumChunks)

	assertdb.Query(t, rt.DB, `SELECT status, error, num_items, num_chunks FROM knowledge_knowledgesource WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "error": nil, "num_items": 3, "num_chunks": 7})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgesource WHERE id = $1 AND last_indexed_on = $2`, k1.ID, indexedOn).Returns(1)

	err = k.SetFailed(ctx, rt.DB, "it went wrong")
	assert.NoError(t, err)
	assert.Equal(t, models.KnowledgeSourceStatusFailed, k.Status)

	assertdb.Query(t, rt.DB, `SELECT status, error FROM knowledge_knowledgesource WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "F", "error": "it went wrong"})

	// errors longer than the column are truncated
	err = k.SetFailed(ctx, rt.DB, strings.Repeat("x", 300))
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT error FROM knowledge_knowledgesource WHERE id = $1`, k1.ID).Returns(strings.Repeat("x", 255))

	// a source Django flagged as pending while we were indexing it stays pending, for the retry cron to index again
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET status = 'P' WHERE id = $1`, k1.ID)

	err = k.SetReady(ctx, rt.DB, indexedOn, 4, 9)
	assert.NoError(t, err)
	assert.Equal(t, models.KnowledgeSourceStatusPending, k.Status)

	assertdb.Query(t, rt.DB, `SELECT status, error, num_items, num_chunks FROM knowledge_knowledgesource WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "P", "error": nil, "num_items": 4, "num_chunks": 9})

	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET status = 'F' WHERE id = $1`, k1.ID)

	// a source released while we were indexing it can't be finalized back to ready
	rt.DB.MustExec(`UPDATE knowledge_knowledgesource SET is_active = FALSE WHERE id = $1`, k1.ID)

	err = k.SetReady(ctx, rt.DB, dates.Now(), 3, 7)
	assert.ErrorIs(t, err, models.ErrKnowledgeSourceReleased)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledgesource WHERE id = $1`, k1.ID).Returns("F")
}

func TestKnowledgeChunks(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	k1 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeSourceTypeShortcuts, "Test", models.KnowledgeSourceStatusPending)

	item1 := uuids.UUID("8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5")
	item2 := uuids.UUID("0e2e1c66-c221-4726-a08a-1a4bbabf05be")

	err := models.InsertKnowledgeChunks(ctx, rt.DB, []*models.KnowledgeChunk{
		{SourceID: k1.ID, ItemKey: item1, ItemName: "Refunds", Text: "We offer full refunds..", Embedding: testEmbedding(1)},
		{SourceID: k1.ID, ItemKey: item1, ItemName: "Refunds", Text: "..within 30 days.", Embedding: testEmbedding(0, 1)},
		{SourceID: k1.ID, ItemKey: item2, ItemName: "Greeting", Text: "Hello! How can we help?", Embedding: testEmbedding(0, 0, 1)},
	})
	assert.NoError(t, err)

	count, err := models.CountKnowledgeChunks(ctx, rt.DB, k1.ID)
	assert.NoError(t, err)
	assert.Equal(t, 3, count)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgechunk WHERE source_id = $1 AND item_key = $2`, k1.ID, item1).Returns(2)

	// embeddings survive the round trip through pgvector's text form
	var embedding models.Embedding
	err = rt.DB.Get(&embedding, `SELECT embedding FROM knowledge_knowledgechunk WHERE source_id = $1 AND item_key = $2`, k1.ID, item2)
	assert.NoError(t, err)
	assert.Equal(t, testEmbedding(0, 0, 1), embedding)

	// deleting by item key only deletes that item's chunks
	err = models.DeleteKnowledgeChunks(ctx, rt.DB, k1.ID, []uuids.UUID{item1})
	assert.NoError(t, err)

	count, err = models.CountKnowledgeChunks(ctx, rt.DB, k1.ID)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	assertdb.Query(t, rt.DB, `SELECT item_key::text FROM knowledge_knowledgechunk WHERE source_id = $1`, k1.ID).Returns(string(item2))
}

func TestLoadChangedShortcuts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	s1 := testdb.InsertShortcut(t, rt, testdb.Org1, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", "Refunds", "We offer full refunds within 30 days.")
	rt.DB.MustExec(`UPDATE tickets_shortcut SET modified_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, s1.ID)

	s2 := testdb.InsertShortcut(t, rt, testdb.Org1, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", "Greeting", "Hello! How can we help?")
	rt.DB.MustExec(`UPDATE tickets_shortcut SET is_active = FALSE, modified_on = NOW() WHERE id = $1`, s2.ID) // released

	testdb.InsertShortcut(t, rt, testdb.Org2, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", "Other", "Other org shortcut.")

	// loading since the zero time returns all of the org's shortcuts, released ones included, oldest change first
	shortcuts, err := models.LoadChangedShortcuts(ctx, rt.DB, testdb.Org1.ID, time.Time{})
	require.NoError(t, err)
	require.Len(t, shortcuts, 2)
	assert.Equal(t, s1.UUID, shortcuts[0].UUID)
	assert.Equal(t, "Refunds", shortcuts[0].Name)
	assert.Equal(t, "We offer full refunds within 30 days.", shortcuts[0].Text)
	assert.True(t, shortcuts[0].IsActive)
	assert.Equal(t, s2.UUID, shortcuts[1].UUID)
	assert.False(t, shortcuts[1].IsActive)

	// loading since an hour ago only returns the recently released shortcut
	shortcuts, err = models.LoadChangedShortcuts(ctx, rt.DB, testdb.Org1.ID, dates.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, shortcuts, 1)
	assert.Equal(t, s2.UUID, shortcuts[0].UUID)

	count, err := models.CountActiveShortcuts(ctx, rt.DB, testdb.Org1.ID)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestLoadChangedArticles(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	k1 := testdb.InsertKnowledgeSource(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeSourceTypeHelpdesk, "Test Helpdesk", models.KnowledgeSourceStatusReady)
	k1s := testdb.InsertSection(t, rt, k1, "a1b2c3d4-0001-4000-8000-000000000001", "General")
	rt.DB.MustExec(`UPDATE knowledge_article SET modified_on = NOW() - INTERVAL '3 hours' WHERE id = $1`, k1s.ID)
	k2 := testdb.InsertKnowledgeSource(t, rt, testdb.Org2, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeSourceTypeHelpdesk, "Other Helpdesk", models.KnowledgeSourceStatusReady)
	k2s := testdb.InsertSection(t, rt, k2, "a1b2c3d4-0002-4000-8000-000000000002", "General")

	a1 := testdb.InsertArticle(t, rt, k1, k1s, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", "Refunds", "Refunds take 5 days.", models.ArticleStatusPublished)
	rt.DB.MustExec(`UPDATE knowledge_article SET modified_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, a1.ID)

	a2 := testdb.InsertArticle(t, rt, k1, k1s, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", "Shipping", "Ships in 2 days.", models.ArticleStatusDraft)
	a3 := testdb.InsertArticle(t, rt, k1, k1s, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", "Hours", "We're open 9 to 5.", models.ArticleStatusPublished)
	rt.DB.MustExec(`UPDATE knowledge_article SET is_active = FALSE, modified_on = NOW() WHERE id = $1`, a3.ID) // released

	testdb.InsertArticle(t, rt, k2, k2s, "df22cbcb-e0e1-4e78-be9f-2e4fbea1b2c3", "Other", "Another helpdesk's article.", models.ArticleStatusPublished)

	// loading since the zero time returns all of the helpdesk's articles - the section, drafts and released ones
	// included - oldest change first
	articles, err := models.LoadChangedArticles(ctx, rt.DB, k1.ID, time.Time{})
	require.NoError(t, err)
	require.Len(t, articles, 4)
	assert.Equal(t, k1s.UUID, articles[0].UUID)
	assert.True(t, articles[0].IsSection())
	assert.False(t, articles[0].Indexable()) // a section is described rather than written, so is never content

	assert.Equal(t, a1.UUID, articles[1].UUID)
	assert.Equal(t, k1.ID, articles[1].SourceID)
	assert.Equal(t, k1s.ID, articles[1].ParentID)
	assert.False(t, articles[1].IsSection())
	assert.Equal(t, "Refunds", articles[1].Title)
	assert.Equal(t, "Refunds take 5 days.", articles[1].Body)
	assert.True(t, articles[1].Indexable())

	// a draft is a tombstone even though it's still active..
	assert.Equal(t, a2.UUID, articles[2].UUID)
	assert.True(t, articles[2].IsActive)
	assert.False(t, articles[2].Indexable())

	// ..as is a released one that was published
	assert.Equal(t, a3.UUID, articles[3].UUID)
	assert.Equal(t, models.ArticleStatusPublished, articles[3].Status)
	assert.False(t, articles[3].Indexable())

	// loading since an hour ago only returns the recently changed articles
	articles, err = models.LoadChangedArticles(ctx, rt.DB, k1.ID, dates.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, articles, 2)
	assert.Equal(t, a2.UUID, articles[0].UUID)
	assert.Equal(t, a3.UUID, articles[1].UUID)

	// only active published articles count as items - not the draft, the released one or the section
	count, err := models.CountPublishedArticles(ctx, rt.DB, k1.ID)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = models.CountPublishedArticles(ctx, rt.DB, k2.ID)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
}
