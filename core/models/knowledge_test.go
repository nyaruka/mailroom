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

func TestGetStaleKnowledge(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// deactivate the system sources baked into the test database so only our test sources are seen
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE`)

	// pending source.. stale
	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Pending", models.KnowledgeStatusPending)

	// pending but released source.. not stale
	k2 := testdb.InsertKnowledge(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeTypeShortcuts, "Released", models.KnowledgeStatusPending)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE WHERE id = $1`, k2.ID)

	// ready source whose org has a shortcut modified since it was indexed.. stale
	k3 := testdb.InsertKnowledge(t, rt, testdb.Org1, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", models.KnowledgeTypeShortcuts, "Stale", models.KnowledgeStatusReady)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET last_indexed_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, k3.ID)

	testdb.InsertShortcut(t, rt, testdb.Org1, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", "Refunds", "We offer full refunds within 30 days.")

	// ready source indexed after the org's last shortcut change.. not stale
	k4 := testdb.InsertKnowledge(t, rt, testdb.Org1, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", models.KnowledgeTypeShortcuts, "Fresh", models.KnowledgeStatusReady)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET last_indexed_on = NOW() WHERE id = $1`, k4.ID)

	// source that failed recently.. not stale yet, the retry interval is the backoff
	testdb.InsertKnowledge(t, rt, testdb.Org1, "9f0b4b7c-3a17-4f5e-95a8-4d68f21e2a7d", models.KnowledgeTypeShortcuts, "Failed", models.KnowledgeStatusFailed)

	// source that failed long enough ago.. stale again, since nothing on the Django side ever re-pends a system
	// source and without this a single embeddings outage would disable it permanently
	k9 := testdb.InsertKnowledge(t, rt, testdb.Org1, "3c5f1e0e-2b4a-4f9c-8d1e-7a6b5c4d3e2f", models.KnowledgeTypeShortcuts, "FailedOld", models.KnowledgeStatusFailed)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET modified_on = NOW() - INTERVAL '30 minutes' WHERE id = $1`, k9.ID)

	// source currently being indexed by a worker.. not stale
	testdb.InsertKnowledge(t, rt, testdb.Org1, "0a1c6a9a-52ed-40cb-a921-1a29b9d8bc6f", models.KnowledgeTypeShortcuts, "Indexing", models.KnowledgeStatusIndexing)

	// source stuck in indexing for over an hour.. stale, the worker that claimed it can only have died
	k7 := testdb.InsertKnowledge(t, rt, testdb.Org1, "639a26a3-8e6e-4ad0-b4b9-6bf7c1cf42d1", models.KnowledgeTypeShortcuts, "Stuck", models.KnowledgeStatusIndexing)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET modified_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, k7.ID)

	// pending source of a type we don't support.. not stale
	testdb.InsertKnowledge(t, rt, testdb.Org2, "df22cbcb-e0e1-4e78-be9f-2e4fbea1b2c3", models.KnowledgeTypeWebsite, "Website", models.KnowledgeStatusPending)

	types := []models.KnowledgeType{models.KnowledgeTypeShortcuts}

	stale, err := models.GetStaleKnowledge(ctx, rt.DB, types, 10)
	require.NoError(t, err)
	require.Len(t, stale, 4)
	assert.Equal(t, k1.ID, stale[0].ID)
	assert.Equal(t, k1.UUID, stale[0].UUID)
	assert.Equal(t, testdb.Org1.ID, stale[0].OrgID)
	assert.Equal(t, models.KnowledgeTypeShortcuts, stale[0].Type)
	assert.Equal(t, models.KnowledgeStatusPending, stale[0].Status)
	assert.Equal(t, k3.ID, stale[1].ID)
	assert.Equal(t, k9.ID, stale[2].ID)
	assert.Equal(t, k7.ID, stale[3].ID)

	// the limit is honoured, taking the lowest ids
	stale, err = models.GetStaleKnowledge(ctx, rt.DB, types, 2)
	require.NoError(t, err)
	require.Len(t, stale, 2)
	assert.Equal(t, k1.ID, stale[0].ID)
	assert.Equal(t, k3.ID, stale[1].ID)

	// and nothing was claimed or otherwise modified by looking
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledge WHERE status = 'I'`).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns("P")
}

func TestClaimKnowledge(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE`)

	types := []models.KnowledgeType{models.KnowledgeTypeShortcuts}

	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Pending", models.KnowledgeStatusPending)

	claimed, err := models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, k1.UUID, types)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, k1.ID, claimed.ID)
	assert.Equal(t, k1.UUID, claimed.UUID)
	assert.Equal(t, testdb.Org1.ID, claimed.OrgID)
	assert.Equal(t, models.KnowledgeTypeShortcuts, claimed.Type)
	assert.Equal(t, models.KnowledgeStatusIndexing, claimed.Status)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns("I")

	// a second trigger for a source being indexed no-ops rather than indexing it twice
	claimed, err = models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, k1.UUID, types)
	require.NoError(t, err)
	assert.Nil(t, claimed)

	// unless the worker that claimed it died without recording an outcome
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET modified_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, k1.ID)

	claimed, err = models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, k1.UUID, types)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, k1.ID, claimed.ID)

	// a released source isn't claimable
	k2 := testdb.InsertKnowledge(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeTypeShortcuts, "Released", models.KnowledgeStatusPending)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE WHERE id = $1`, k2.ID)

	claimed, err = models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, k2.UUID, types)
	require.NoError(t, err)
	assert.Nil(t, claimed)

	// nor is one of a type we can't index
	k3 := testdb.InsertKnowledge(t, rt, testdb.Org1, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", models.KnowledgeTypeWebsite, "Website", models.KnowledgeStatusPending)

	claimed, err = models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, k3.UUID, types)
	require.NoError(t, err)
	assert.Nil(t, claimed)

	// nor one belonging to a different org
	k4 := testdb.InsertKnowledge(t, rt, testdb.Org2, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", models.KnowledgeTypeShortcuts, "Other Org", models.KnowledgeStatusPending)

	claimed, err = models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, k4.UUID, types)
	require.NoError(t, err)
	assert.Nil(t, claimed)

	// and a source that doesn't exist is just a no-op
	claimed, err = models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, "9f0b4b7c-3a17-4f5e-95a8-4d68f21e2a7d", types)
	require.NoError(t, err)
	assert.Nil(t, claimed)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k2.ID).Returns("P")
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k3.ID).Returns("P")
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k4.ID).Returns("P")
}

func TestSetReadyAndSetFailed(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE`)

	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Test", models.KnowledgeStatusPending)

	k, err := models.ClaimKnowledge(ctx, rt.DB, testdb.Org1.ID, k1.UUID, []models.KnowledgeType{models.KnowledgeTypeShortcuts})
	require.NoError(t, err)
	require.NotNil(t, k)

	indexedOn := dates.Now().In(time.UTC).Truncate(time.Millisecond)
	err = k.SetReady(ctx, rt.DB, indexedOn, 3, 7)
	assert.NoError(t, err)
	assert.Equal(t, models.KnowledgeStatusReady, k.Status)
	assert.Equal(t, 3, k.NumItems)
	assert.Equal(t, 7, k.NumChunks)

	assertdb.Query(t, rt.DB, `SELECT status, error, num_items, num_chunks FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "error": nil, "num_items": 3, "num_chunks": 7})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledge WHERE id = $1 AND last_indexed_on = $2`, k1.ID, indexedOn).Returns(1)

	err = k.SetFailed(ctx, rt.DB, "it went wrong")
	assert.NoError(t, err)
	assert.Equal(t, models.KnowledgeStatusFailed, k.Status)

	assertdb.Query(t, rt.DB, `SELECT status, error FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "F", "error": "it went wrong"})

	// errors longer than the column are truncated
	err = k.SetFailed(ctx, rt.DB, strings.Repeat("x", 300))
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT error FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns(strings.Repeat("x", 255))

	// a source released while we were indexing it can't be finalized back to ready
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE WHERE id = $1`, k1.ID)

	err = k.SetReady(ctx, rt.DB, dates.Now(), 3, 7)
	assert.ErrorIs(t, err, models.ErrKnowledgeReleased)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns("F")
}

func TestKnowledgeChunks(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Test", models.KnowledgeStatusPending)

	item1 := uuids.UUID("8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5")
	item2 := uuids.UUID("0e2e1c66-c221-4726-a08a-1a4bbabf05be")

	err := models.InsertKnowledgeChunks(ctx, rt.DB, []*models.KnowledgeChunk{
		{KnowledgeID: k1.ID, ItemKey: item1, ItemName: "Refunds", Text: "We offer full refunds..", Embedding: testEmbedding(1)},
		{KnowledgeID: k1.ID, ItemKey: item1, ItemName: "Refunds", Text: "..within 30 days.", Embedding: testEmbedding(0, 1)},
		{KnowledgeID: k1.ID, ItemKey: item2, ItemName: "Greeting", Text: "Hello! How can we help?", Embedding: testEmbedding(0, 0, 1)},
	})
	assert.NoError(t, err)

	count, err := models.CountKnowledgeChunks(ctx, rt.DB, k1.ID)
	assert.NoError(t, err)
	assert.Equal(t, 3, count)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgechunk WHERE knowledge_id = $1 AND item_key = $2`, k1.ID, item1).Returns(2)

	// embeddings survive the round trip through pgvector's text form
	var embedding models.Embedding
	err = rt.DB.Get(&embedding, `SELECT embedding FROM knowledge_knowledgechunk WHERE knowledge_id = $1 AND item_key = $2`, k1.ID, item2)
	assert.NoError(t, err)
	assert.Equal(t, testEmbedding(0, 0, 1), embedding)

	// deleting by item key only deletes that item's chunks
	err = models.DeleteKnowledgeChunks(ctx, rt.DB, k1.ID, []uuids.UUID{item1})
	assert.NoError(t, err)

	count, err = models.CountKnowledgeChunks(ctx, rt.DB, k1.ID)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	assertdb.Query(t, rt.DB, `SELECT item_key::text FROM knowledge_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns(string(item2))
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
