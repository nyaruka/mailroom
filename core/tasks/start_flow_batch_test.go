package tasks_test

import (
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/centrifugo"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	_ "github.com/nyaruka/mailroom/v26/core/runner/handlers"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartFlowBatchTask(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	// someone has the flow open in the editor, so progress is published to its socket
	_, err := vc.Do("SET", centrifugo.SubscriptionKey(models.FlowSocket(testdb.SingleMessage.UUID)), "1")
	require.NoError(t, err)

	progress := func(start *models.FlowStart, status string, current int) string {
		return fmt.Sprintf(`{"type": "start_progress", "start_uuid": "%s", "status": %q, "progress": {"current": %d, "total": 4}}`, start.UUID, status, current)
	}
	assertPublished := func(expected ...string) {
		sent := testsuite.CentrifugoHistory(t, rt, models.FlowSocket(testdb.SingleMessage.UUID))
		require.Len(t, sent, len(expected))
		for i, e := range expected {
			assert.JSONEq(t, e, string(sent[i]), "published event %d mismatch", i)
		}
	}

	// create a start
	start1 := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID})
	err = models.InsertFlowStart(ctx, rt.DB, start1)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start1.ID).Returns("P")

	start1BatchTask := tasks.BatchTask{BatchOwnerUUID: start1.UUID, TotalBatches: 2}
	batch1 := start1.CreateBatch([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, 4)
	batch2 := start1.CreateBatch([]models.ContactID{testdb.Cat.ID, testdb.Dan.ID}, 4)

	// start the first batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &tasks.StartFlowBatch{BatchTask: start1BatchTask, FlowStartBatch: batch1}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = ANY($1) 
		AND status = 'C' AND call_uuid IS NULL AND output IS NOT NULL`, pq.Array([]core.ContactUUID{testdb.Ann.UUID, testdb.Bob.UUID})).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE contact_id = ANY($1) and flow_id = $2 AND responded = FALSE AND org_id = 1 AND status = 'C'
		AND results IS NOT NULL AND path_nodes IS NOT NULL AND session_uuid IS NOT NULL`, pq.Array([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID}), testdb.SingleMessage.ID).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = ANY($1) AND text = 'Hey, how are you?' AND org_id = 1 AND status = 'Q' 
		AND direction = 'O' AND msg_type = 'T'`, pq.Array([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID})).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start1.ID).Returns("S")

	// watchers were told the start began and then how far the first batch took it
	assertPublished(progress(start1, "started", 0), progress(start1, "started", 2))

	// start the second and final batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &tasks.StartFlowBatch{BatchTask: start1BatchTask, FlowStartBatch: batch2}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE start_id = $1`, start1.ID).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start1.ID).Returns("C")

	// and that it finished
	assertPublished(progress(start1, "started", 0), progress(start1, "started", 2), progress(start1, "completed", 4))

	// create a second start
	start2 := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID})
	err = models.InsertFlowStart(ctx, rt.DB, start2)
	require.NoError(t, err)

	start2BatchTask := tasks.BatchTask{BatchOwnerUUID: start2.UUID, TotalBatches: 2}
	start2Batch1 := start2.CreateBatch([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, 4)
	start2Batch2 := start2.CreateBatch([]models.ContactID{testdb.Cat.ID, testdb.Dan.ID}, 4)

	// start the first batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &tasks.StartFlowBatch{BatchTask: start2BatchTask, FlowStartBatch: start2Batch1}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE start_id = $1`, start2.ID).Returns(2)

	// interrupt the start
	rt.DB.MustExec(`UPDATE flows_flowstart SET status = 'I' WHERE id = $1`, start2.ID)

	// start the second batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &tasks.StartFlowBatch{BatchTask: start2BatchTask, FlowStartBatch: start2Batch2}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	// check that second batch didn't create any runs and start status is still interrupted
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE start_id = $1`, start2.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start2.ID).Returns("I")

	// watchers of the second start saw it begin, progress and then get interrupted
	assertPublished(
		progress(start1, "started", 0), progress(start1, "started", 2), progress(start1, "completed", 4),
		progress(start2, "started", 0), progress(start2, "started", 2), progress(start2, "interrupted", 2),
	)

	// starts from scheduled triggers aren't seen by users so their progress isn't published
	start3 := models.NewFlowStart(models.OrgID(1), models.StartTypeTrigger, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID})
	err = models.InsertFlowStart(ctx, rt.DB, start3)
	require.NoError(t, err)

	start3Batch := start3.CreateBatch([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, 2)
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &tasks.StartFlowBatch{BatchTask: tasks.BatchTask{BatchOwnerUUID: start3.UUID, TotalBatches: 1}, FlowStartBatch: start3Batch}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start3.ID).Returns("C")
	assert.Len(t, testsuite.CentrifugoHistory(t, rt, models.FlowSocket(testdb.SingleMessage.UUID)), 6)
}

func TestStartFlowBatchTaskNonPersistedStart(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// create a start
	start := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID})

	batch := start.CreateBatch([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, 2)

	// start the first batch...
	batchTask := tasks.BatchTask{BatchOwnerUUID: start.UUID, TotalBatches: 1}
	err := tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &tasks.StartFlowBatch{BatchTask: batchTask, FlowStartBatch: batch}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(2)
}
