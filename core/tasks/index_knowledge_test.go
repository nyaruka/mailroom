package tasks_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// deactivate the system sources baked into the test database so only our test sources are indexable
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE`)

	oa := testdb.Org1.Load(t, rt)

	// org1 has a pending shortcuts source, two active shortcuts and one already released
	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Test Shortcuts", models.KnowledgeStatusPending)
	s1 := testdb.InsertShortcut(t, rt, testdb.Org1, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", "Refunds", "We offer full refunds within 30 days.")
	s2 := testdb.InsertShortcut(t, rt, testdb.Org1, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", "Greeting", "Hello! How can we help?")
	s3 := testdb.InsertShortcut(t, rt, testdb.Org1, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", "Old", "This shortcut is gone.")
	rt.DB.MustExec(`UPDATE tickets_shortcut SET is_active = FALSE, name = 'deleted-b26e0a76' WHERE id = $1`, s3.ID)

	task := &tasks.IndexKnowledge{KnowledgeUUID: k1.UUID}

	// without an embeddings service configured the task is a no-op
	err := task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns("P")

	rt.Config.EmbeddingsEndpoint = "http://embeddings:8095/v1"

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://embeddings:8095/v1/embeddings": {
			mockEmbeddingsResponse(2),
			mockEmbeddingsResponse(1),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
		},
	})
	rt.HTTP.Services.Transport = mocks

	// the source is claimed, chunked and embedded, leaving it ready and searchable
	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status, error, num_items, num_chunks FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "error": nil, "num_items": 2, "num_chunks": 2})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledge WHERE id = $1 AND last_indexed_on IS NOT NULL`, k1.ID).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT item_name, text FROM knowledge_knowledgechunk WHERE knowledge_id = $1 AND item_key = $2`, k1.ID, s1.UUID).
		Columns(map[string]any{"item_name": "Refunds", "text": "We offer full refunds within 30 days."})

	// age the shortcuts past the watermark margin the indexer subtracts, so that they're no longer changed
	rt.DB.MustExec(`UPDATE tickets_shortcut SET modified_on = NOW() - INTERVAL '1 minute' WHERE org_id = $1`, testdb.Org1.ID)

	// running again with nothing changed re-finalizes the source without embedding anything
	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status, num_items, num_chunks FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "num_items": 2, "num_chunks": 2})

	// a trigger arriving while another worker is indexing the source is a no-op
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET status = 'I', modified_on = NOW() WHERE id = $1`, k1.ID)

	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns("I")

	rt.DB.MustExec(`UPDATE knowledge_knowledge SET status = 'R' WHERE id = $1`, k1.ID)

	// now edit one shortcut and release another..
	rt.DB.MustExec(`UPDATE tickets_shortcut SET text = 'We no longer offer refunds.', modified_on = NOW() WHERE id = $1`, s1.ID)
	rt.DB.MustExec(`UPDATE tickets_shortcut SET is_active = FALSE, name = 'deleted-0e2e1c66', modified_on = NOW() WHERE id = $1`, s2.ID)

	// ..and the next run re-indexes just the changed items
	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status, num_items, num_chunks FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "num_items": 1, "num_chunks": 1})
	assertdb.Query(t, rt.DB, `SELECT text FROM knowledge_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns("We no longer offer refunds.")

	// an error from the embeddings service must leave the source failed with its error recorded.. never stuck indexing
	rt.DB.MustExec(`UPDATE tickets_shortcut SET modified_on = NOW() WHERE id = $1`, s1.ID)

	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.EqualError(t, err, fmt.Sprintf(`error indexing knowledge source %d: error embedding chunks: error calling embeddings endpoint, got non-200 status: {"error": "oops"}`, k1.ID))

	assertdb.Query(t, rt.DB, `SELECT status, error FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "F", "error": `error embedding chunks: error calling embeddings endpoint, got non-200 status: {"error": "oops"}`})

	// but the chunks from the last successful index are still there to be searched
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns(1)

	require.False(t, mocks.HasUnused())
}
