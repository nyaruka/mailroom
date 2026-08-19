package tasks_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/locks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	embedder := &testsuite.MockEmbedder{}
	rt.Embeddings = embedder

	// the source is chunked and embedded, leaving it ready and searchable
	err := task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status, error, num_items, num_chunks FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "error": nil, "num_items": 2, "num_chunks": 2})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledge WHERE id = $1 AND last_indexed_on IS NOT NULL`, k1.ID).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT item_name, text FROM knowledge_knowledgechunk WHERE knowledge_id = $1 AND item_key = $2`, k1.ID, s1.UUID).
		Columns(map[string]any{"item_name": "Refunds", "text": "We offer full refunds within 30 days."})

	// the shortcuts were embedded as passages, not as queries
	assert.Equal(t, []string{"We offer full refunds within 30 days.", "Hello! How can we help?"}, embedder.Passages)
	assert.Empty(t, embedder.Queries)

	// age the shortcuts past the watermark margin the indexer subtracts, so that they're no longer changed
	rt.DB.MustExec(`UPDATE tickets_shortcut SET modified_on = NOW() - INTERVAL '1 minute' WHERE org_id = $1`, testdb.Org1.ID)

	// running again with nothing changed re-finalizes the source without embedding anything
	embedder.Passages = nil

	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assert.Empty(t, embedder.Passages)

	assertdb.Query(t, rt.DB, `SELECT status, num_items, num_chunks FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "R", "num_items": 2, "num_chunks": 2})

	// a trigger arriving while another worker holds the source's lock is a no-op
	locker := locks.NewLocker(fmt.Sprintf("lock:knowledge:%s", k1.UUID), time.Minute)
	lock, err := locker.Grab(ctx, rt.VK, 0)
	require.NoError(t, err)
	require.NotEmpty(t, lock)

	rt.DB.MustExec(`UPDATE tickets_shortcut SET modified_on = NOW() WHERE id = $1`, s1.ID)

	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns("R")

	require.NoError(t, locker.Release(ctx, rt.VK, lock))

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
	embedder.Error = errors.New("embeddings service is down")

	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.EqualError(t, err, fmt.Sprintf(`error indexing knowledge source %d: error embedding chunks: embeddings service is down`, k1.ID))

	assertdb.Query(t, rt.DB, `SELECT status, error FROM knowledge_knowledge WHERE id = $1`, k1.ID).
		Columns(map[string]any{"status": "F", "error": "error embedding chunks: embeddings service is down"})

	// but the chunks from the last successful index are still there to be searched
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM knowledge_knowledgechunk WHERE knowledge_id = $1`, k1.ID).Returns(1)

	// a source of a type we can't index yet is a no-op rather than an error
	k2 := testdb.InsertKnowledge(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeTypeWebsite, "Website", models.KnowledgeStatusPending)

	err = (&tasks.IndexKnowledge{KnowledgeUUID: k2.UUID}).Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k2.ID).Returns("P")

	// as is one that's been released
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE WHERE id = $1`, k1.ID)

	err = task.Perform(ctx, rt, oa, testTaskID)
	assert.NoError(t, err)
}
