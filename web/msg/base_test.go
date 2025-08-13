package msg_test

import (
	"testing"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestSend(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Date(2015, 1, 1, 12, 30, 45, 0, time.UTC), nil)

	testsuite.RunWebTests(t, ctx, rt, "testdata/send.json", testsuite.ResetNone)

	testsuite.AssertCourierQueues(t, rt, map[string][]int{"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1, 1, 1, 1}})
}

func TestHandle(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hello", models.MsgStatusHandled)
	testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hello", models.MsgStatusPending)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "how can we help", nil, models.MsgStatusSent, false)

	testsuite.RunWebTests(t, ctx, rt, "testdata/handle.json", testsuite.ResetValkey)
}

func TestResend(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hello", models.MsgStatusHandled)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "how can we help", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Bob, "this failed", nil, models.MsgStatusFailed, false)
	georgeOut := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.George, "no URN", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL WHERE id = $1`, georgeOut.ID)

	testsuite.RunWebTests(t, ctx, rt, "testdata/resend.json", testsuite.ResetNone)
}

func TestBroadcast(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testdb.InsertOptIn(rt, testdb.Org1, "Polls")

	createRun := func(org *testdb.Org, contact *testdb.Contact, nodeUUID flows.NodeUUID) {
		sessionUUID := testdb.InsertFlowSession(rt, contact, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, nil)
		testdb.InsertFlowRun(rt, org, sessionUUID, contact, testdb.Favorites, models.RunStatusWaiting, nodeUUID)
	}

	// put bob and george in a flows at different nodes
	createRun(testdb.Org1, testdb.Bob, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdb.Org1, testdb.George, "a52a9e6d-34bb-4be1-8034-99e33d0862c6")

	testsuite.RunWebTests(t, ctx, rt, "testdata/broadcast.json", testsuite.ResetValkey)
}

func TestBroadcastPreview(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/broadcast_preview.json", testsuite.ResetNone)
}
