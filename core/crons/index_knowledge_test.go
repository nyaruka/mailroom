package crons_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

// creates a mock embeddings service response with the given number of 384 dimension embeddings
func mockEmbeddingsResponse(count int) *httpx.MockResponse {
	data := make([]map[string]any, count)
	for i := range count {
		embedding := make([]float32, 384)
		embedding[i] = 1
		data[i] = map[string]any{"index": i, "embedding": embedding}
	}
	return httpx.NewMockResponse(200, nil, jsonx.MustMarshal(map[string]any{"data": data}))
}

func TestIndexKnowledge(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	// deactivate the system sources baked into the test database so only our test sources are swept
	rt.DB.MustExec(`UPDATE tickets_knowledge SET is_active = FALSE`)

	cron := &crons.IndexKnowledgeCron{BatchSize: 25}

	// without an embeddings service configured the cron is a no-op
	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"indexed": 0, "failed": 0}, res)

	rt.Config.EmbeddingsEndpoint = "http://embeddings:8095/v1"

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://embeddings:8095/v1/embeddings": {
			mockEmbeddingsResponse(2),
			mockEmbeddingsResponse(1),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
		},
	})
	rt.HTTP.Services.Transport = mocks

	// org1 has a pending shortcuts source, two active shortcuts and one already released
	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Test Shortcuts", models.KnowledgeStatusPending)
	s1 := testdb.InsertShortcut(t, rt, testdb.Org1, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", "Refunds", "We offer full refunds within 30 days.")
	s2 := testdb.InsertShortcut(t, rt, testdb.Org1, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", "Greeting", "Hello! How can we help?")
	s3 := testdb.InsertShortcut(t, rt, testdb.Org1, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", "Old", "This shortcut is gone.")
	rt.DB.MustExec(`UPDATE tickets_shortcut SET is_active = FALSE, name = 'deleted-b26e0a76' WHERE id = $1`, s3.ID)

	// org2 has a shortcut and a source indexed since it changed
	testdb.InsertShortcut(t, rt, testdb.Org2, "df22cbcb-e0e1-4e78-be9f-2e4fbea1b2c3", "Help", "Have you tried turning it off and on again?")
	k2 := testdb.InsertKnowledge(t, rt, testdb.Org2, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeTypeShortcuts, "Test Shortcuts", models.KnowledgeStatusReady)
	rt.DB.MustExec(`UPDATE tickets_knowledge SET last_indexed_on = NOW() WHERE id = $1`, k2.ID)

	// first sweep indexes org1's pending source - chunking and embedding its two active shortcuts
	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"indexed": 1, "failed": 0}, res)

	assertdb.Query(t, rt.DB, `SELECT status, error, num_items, num_chunks FROM tickets_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "error": nil, "num_items": 2, "num_chunks": 2})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_knowledge WHERE id = $1 AND last_indexed_on IS NOT NULL`, k1.ID).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT item_name, text FROM tickets_knowledgechunk WHERE knowledge_id = $1 AND item_key = $2`, k1.ID, s1.UUID).
		Columns(map[string]any{"item_name": "Refunds", "text": "We offer full refunds within 30 days."})

	// org2's source was fresh so wasn't touched
	assertdb.Query(t, rt.DB, `SELECT status, num_chunks FROM tickets_knowledge WHERE id = $1`, k2.ID).
		Columns(map[string]any{"status": "R", "num_chunks": 0})

	// now edit one shortcut and release another..
	rt.DB.MustExec(`UPDATE tickets_shortcut SET text = 'We no longer offer refunds.', modified_on = NOW() WHERE id = $1`, s1.ID)
	rt.DB.MustExec(`UPDATE tickets_shortcut SET is_active = FALSE, name = 'deleted-0e2e1c66', modified_on = NOW() WHERE id = $1`, s2.ID)

	// ..and the next sweep sees org1's source as stale and re-indexes just the changed items
	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"indexed": 1, "failed": 0}, res)

	assertdb.Query(t, rt.DB, `SELECT status, num_items, num_chunks FROM tickets_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "num_items": 1, "num_chunks": 1})
	assertdb.Query(t, rt.DB, `SELECT text FROM tickets_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns("We no longer offer refunds.")

	// an error from the embeddings service must leave a source failed with its error recorded.. never stuck in indexing
	rt.DB.MustExec(`UPDATE tickets_shortcut SET modified_on = NOW() WHERE org_id = $1`, testdb.Org2.ID)
	rt.DB.MustExec(`UPDATE tickets_knowledge SET status = 'P' WHERE id = $1`, k2.ID)

	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"indexed": 0, "failed": 1}, res)

	assertdb.Query(t, rt.DB, `SELECT status, error FROM tickets_knowledge WHERE id = $1`, k2.ID).
		Columns(map[string]any{"status": "F", "error": `error embedding chunks: error calling embeddings endpoint, got non-200 status: {"error": "oops"}`})

	assert.False(t, mocks.HasUnused())
}
