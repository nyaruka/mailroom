package msg_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestSend(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	cathyTicket := testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Date(2015, 1, 1, 12, 30, 45, 0, time.UTC), nil)

	testsuite.RunWebTests(t, ctx, rt, "testdata/send.json", map[string]string{
		"cathy_ticket_id": fmt.Sprint(cathyTicket.ID),
	})

	testsuite.AssertCourierQueues(t, map[string][]int{"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1, 1, 1, 1}})
}

func TestHandle(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	cathyIn1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hello", models.MsgStatusHandled)
	cathyIn2 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hello", models.MsgStatusPending)
	cathyOut := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "how can we help", nil, models.MsgStatusSent, false)

	testsuite.RunWebTests(t, ctx, rt, "testdata/handle.json", map[string]string{
		"cathy_msgin1_id": fmt.Sprint(cathyIn1.ID),
		"cathy_msgin2_id": fmt.Sprint(cathyIn2.ID),
		"cathy_msgout_id": fmt.Sprint(cathyOut.ID),
	})

	orgTasks := testsuite.CurrentTasks(t, rt, "handler")[testdb.Org1.ID]
	assert.Len(t, orgTasks, 1)
	assert.Equal(t, "handle_contact_event", orgTasks[0].Type)
}

func TestResend(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	cathyIn := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hello", models.MsgStatusHandled)
	cathyOut := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "how can we help", nil, models.MsgStatusSent, false)
	bobOut := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Bob, "this failed", nil, models.MsgStatusFailed, false)
	georgeOut := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.George, "no URN", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL WHERE id = $1`, georgeOut.ID)

	testsuite.RunWebTests(t, ctx, rt, "testdata/resend.json", map[string]string{
		"cathy_msgin_id":   fmt.Sprint(cathyIn.ID),
		"cathy_msgout_id":  fmt.Sprint(cathyOut.ID),
		"bob_msgout_id":    fmt.Sprint(bobOut.ID),
		"george_msgout_id": fmt.Sprint(georgeOut.ID),
	})
}

func TestBroadcast(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	polls := testdb.InsertOptIn(rt, testdb.Org1, "Polls")

	createRun := func(org *testdb.Org, contact *testdb.Contact, nodeUUID flows.NodeUUID) {
		sessionUUID := testdb.InsertFlowSession(rt, contact, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, models.NilCallID)
		testdb.InsertFlowRun(rt, org, sessionUUID, contact, testdb.Favorites, models.RunStatusWaiting, nodeUUID)
	}

	// put bob and george in a flows at different nodes
	createRun(testdb.Org1, testdb.Bob, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdb.Org1, testdb.George, "a52a9e6d-34bb-4be1-8034-99e33d0862c6")

	testsuite.RunWebTests(t, ctx, rt, "testdata/broadcast.json", map[string]string{
		"polls_id": fmt.Sprint(polls.ID),
	})

	testsuite.AssertBatchTasks(t, testdb.Org1.ID, map[string]int{"send_broadcast": 2})
}

func TestBroadcastPreview(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/broadcast_preview.json", nil)
}
