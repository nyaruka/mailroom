package starts_test

import (
	"testing"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/starts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartFlowBatchTask(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// create a start
	start1 := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID, testdb.George.ID, testdb.Alexandra.ID})
	err := models.InsertFlowStart(ctx, rt.DB, start1)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start1.ID).Returns("P")

	batch1 := start1.CreateBatch([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID}, true, false, 4)
	batch2 := start1.CreateBatch([]models.ContactID{testdb.George.ID, testdb.Alexandra.ID}, false, true, 4)

	// start the first batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &starts.StartFlowBatchTask{FlowStartBatch: batch1}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = ANY($1) 
		AND status = 'C' AND call_id IS NULL AND output IS NOT NULL`, pq.Array([]flows.ContactUUID{testdb.Cathy.UUID, testdb.Bob.UUID})).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE contact_id = ANY($1) and flow_id = $2 AND responded = FALSE AND org_id = 1 AND status = 'C'
		AND results IS NOT NULL AND path_nodes IS NOT NULL AND session_uuid IS NOT NULL`, pq.Array([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID}), testdb.SingleMessage.ID).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = ANY($1) AND text = 'Hey, how are you?' AND org_id = 1 AND status = 'Q' 
		AND direction = 'O' AND msg_type = 'T'`, pq.Array([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID})).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start1.ID).Returns("S")

	// start the second and final batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &starts.StartFlowBatchTask{FlowStartBatch: batch2}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE start_id = $1`, start1.ID).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start1.ID).Returns("C")

	// create a second start
	start2 := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID, testdb.George.ID, testdb.Alexandra.ID})
	err = models.InsertFlowStart(ctx, rt.DB, start2)
	require.NoError(t, err)

	start2Batch1 := start2.CreateBatch([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID}, true, false, 4)
	start2Batch2 := start2.CreateBatch([]models.ContactID{testdb.George.ID, testdb.Alexandra.ID}, false, true, 4)

	// start the first batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &starts.StartFlowBatchTask{FlowStartBatch: start2Batch1}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE start_id = $1`, start2.ID).Returns(2)

	// interrupt the start
	rt.DB.MustExec(`UPDATE flows_flowstart SET status = 'I' WHERE id = $1`, start2.ID)

	// start the second batch...
	err = tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &starts.StartFlowBatchTask{FlowStartBatch: start2Batch2}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	// check that second batch didn't create any runs and start status is still interrupted
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE start_id = $1`, start2.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, start2.ID).Returns("I")
}

func TestStartFlowBatchTaskNonPersistedStart(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	// create a start
	start := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID, testdb.George.ID, testdb.Alexandra.ID})

	batch := start.CreateBatch([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID}, true, true, 2)

	// start the first batch...
	err := tasks.Queue(ctx, rt, rt.Queues.Throttled, testdb.Org1.ID, &starts.StartFlowBatchTask{FlowStartBatch: batch}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(2)
}
