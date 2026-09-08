package crons_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestRetryKnowledgeIndexing(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// deactivate the system sources baked into the test database so only our test sources are swept
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET is_active = FALSE`)

	cron := &crons.RetryKnowledgeIndexingCron{BatchSize: 25}

	// org1 has a pending source, and a source stuck in indexing since the worker that claimed it died
	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Test Shortcuts", models.KnowledgeStatusPending)
	k2 := testdb.InsertKnowledge(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeTypeShortcuts, "Stuck", models.KnowledgeStatusIndexing)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET modified_on = NOW() - INTERVAL '2 hours' WHERE id = $1`, k2.ID)

	// org2 has a source indexed since its shortcuts last changed
	testdb.InsertShortcut(t, rt, testdb.Org2, "df22cbcb-e0e1-4e78-be9f-2e4fbea1b2c3", "Help", "Have you tried turning it off and on again?")
	k3 := testdb.InsertKnowledge(t, rt, testdb.Org2, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", models.KnowledgeTypeShortcuts, "Test Shortcuts", models.KnowledgeStatusReady)
	rt.DB.MustExec(`UPDATE knowledge_knowledge SET last_indexed_on = NOW() WHERE id = $1`, k3.ID)

	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"queued": 2}, res)

	assert.Equal(t, map[string][]string{"batch/1": {"index_knowledge", "index_knowledge"}}, testsuite.GetQueuedTaskTypes(t, rt))

	// claiming is the task's job so the sources are untouched by the sweep itself
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k1.ID).Returns("P")
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k2.ID).Returns("I")
	assertdb.Query(t, rt.DB, `SELECT status FROM knowledge_knowledge WHERE id = $1`, k3.ID).Returns("R")

	testsuite.ClearTasks(t, rt)

	// org2's source becomes stale when one of its shortcuts changes
	rt.DB.MustExec(`UPDATE tickets_shortcut SET text = 'Have you tried turning it on and off again?', modified_on = NOW() WHERE org_id = $1`, testdb.Org2.ID)

	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"queued": 3}, res)

	assert.Equal(t, map[string][]string{
		"batch/1": {"index_knowledge", "index_knowledge"},
		"batch/2": {"index_knowledge"},
	}, testsuite.GetQueuedTaskTypes(t, rt))
}
