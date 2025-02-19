package ivr_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/ivr"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestBulkCallHangup(t *testing.T) {
	_, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	// create voice session for Cathy
	call1ID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy)
	s1ID := testdata.InsertWaitingSession(rt, testdata.Cathy, models.FlowTypeVoice, testdata.IVRFlow, call1ID)
	r1ID := testdata.InsertFlowRun(rt, testdata.Org1, s1ID, testdata.Cathy, testdata.Favorites, models.RunStatusWaiting, "")

	// create voice session for Bob with expiration in future
	call2ID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Bob)
	s2ID := testdata.InsertWaitingSession(rt, testdata.Bob, models.FlowTypeMessaging, testdata.IVRFlow, call2ID)
	r2ID := testdata.InsertFlowRun(rt, testdata.Org1, s2ID, testdata.Bob, testdata.IVRFlow, models.RunStatusWaiting, "")

	// create a messaging session for Alexandria
	s3ID := testdata.InsertWaitingSession(rt, testdata.Alexandria, models.FlowTypeMessaging, testdata.Favorites, models.NilCallID)
	r3ID := testdata.InsertFlowRun(rt, testdata.Org1, s3ID, testdata.Alexandria, testdata.Favorites, models.RunStatusWaiting, "")

	testsuite.QueueBatchTask(t, rt, testdata.Org1, &ivr.BulkCallHangupTask{
		Hangups: []*ivr.Hangup{
			{SessionID: s1ID, CallID: call1ID},
		},
	})
	assert.Equal(t, map[string]int{"bulk_call_hangup": 1}, testsuite.FlushTasks(t, rt, "batch"))

	// Cathy's session should be expired along with its runs
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE id = $1;`, s1ID).Columns(map[string]any{"status": "X"})
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1;`, r1ID).Columns(map[string]any{"status": "X"})

	// Bob's session and run should be unchanged
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE id = $1;`, s2ID).Columns(map[string]any{"status": "W"})
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1;`, r2ID).Columns(map[string]any{"status": "W"})

	// Alexandria's session and run should be unchanged because message expirations are handled separately
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE id = $1;`, s3ID).Columns(map[string]any{"status": "W"})
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1;`, r3ID).Columns(map[string]any{"status": "W"})
}
