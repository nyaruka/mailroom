package msg_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestSend(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	cathyTicket := testdata.InsertOpenTicket(rt, testdata.Org1, testdata.Cathy, testdata.DefaultTopic, time.Date(2015, 1, 1, 12, 30, 45, 0, time.UTC), nil)

	testsuite.RunWebTests(t, ctx, rt, "testdata/send.json", map[string]string{
		"cathy_ticket_id": fmt.Sprintf("%d", cathyTicket.ID),
	})

	testsuite.AssertCourierQueues(t, map[string][]int{"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1, 1, 1, 1}})
}

func TestHandle(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	cathyIn1 := testdata.InsertIncomingMsg(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy, "hello", models.MsgStatusHandled)
	cathyIn2 := testdata.InsertIncomingMsg(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy, "hello", models.MsgStatusPending)
	cathyOut := testdata.InsertOutgoingMsg(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy, "how can we help", nil, models.MsgStatusSent, false)

	testsuite.RunWebTests(t, ctx, rt, "testdata/handle.json", map[string]string{
		"cathy_msgin1_id": fmt.Sprintf("%d", cathyIn1.ID),
		"cathy_msgin2_id": fmt.Sprintf("%d", cathyIn2.ID),
		"cathy_msgout_id": fmt.Sprintf("%d", cathyOut.ID),
	})

	orgTasks := testsuite.CurrentTasks(t, rt, "handler")[testdata.Org1.ID]
	assert.Len(t, orgTasks, 1)
	assert.Equal(t, "handle_contact_event", orgTasks[0].Type)
}

func TestResend(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	cathyIn := testdata.InsertIncomingMsg(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy, "hello", models.MsgStatusHandled)
	cathyOut := testdata.InsertOutgoingMsg(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy, "how can we help", nil, models.MsgStatusSent, false)
	bobOut := testdata.InsertOutgoingMsg(rt, testdata.Org1, testdata.VonageChannel, testdata.Bob, "this failed", nil, models.MsgStatusFailed, false)
	georgeOut := testdata.InsertOutgoingMsg(rt, testdata.Org1, testdata.VonageChannel, testdata.George, "no URN", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL WHERE id = $1`, georgeOut.ID)

	testsuite.RunWebTests(t, ctx, rt, "testdata/resend.json", map[string]string{
		"cathy_msgin_id":   fmt.Sprintf("%d", cathyIn.ID),
		"cathy_msgout_id":  fmt.Sprintf("%d", cathyOut.ID),
		"bob_msgout_id":    fmt.Sprintf("%d", bobOut.ID),
		"george_msgout_id": fmt.Sprintf("%d", georgeOut.ID),
	})
}

func TestBroadcast(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	polls := testdata.InsertOptIn(rt, testdata.Org1, "Polls")

	createRun := func(org *testdata.Org, contact *testdata.Contact, nodeUUID flows.NodeUUID) {
		sessionID := testdata.InsertFlowSession(rt, contact, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)
		testdata.InsertFlowRun(rt, org, sessionID, contact, testdata.Favorites, models.RunStatusWaiting, nodeUUID)
	}

	// put bob and george in a flows at different nodes
	createRun(testdata.Org1, testdata.Bob, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdata.Org1, testdata.George, "a52a9e6d-34bb-4be1-8034-99e33d0862c6")

	testsuite.RunWebTests(t, ctx, rt, "testdata/broadcast.json", map[string]string{
		"polls_id": fmt.Sprintf("%d", polls.ID),
	})

	testsuite.AssertBatchTasks(t, testdata.Org1.ID, map[string]int{"send_broadcast": 2})
}

func TestBroadcastPreview(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/broadcast_preview.json", nil)
}
