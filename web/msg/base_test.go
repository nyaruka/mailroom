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
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Date(2015, 1, 1, 12, 30, 45, 0, time.UTC), nil)

	testsuite.RunWebTests(t, rt, "testdata/send.json")

	testsuite.AssertCourierQueues(t, rt, map[string][]int{"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1, 1, 1, 1}})
}

func TestHandle(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusHandled)
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusPending)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "how can we help", nil, models.MsgStatusSent, false)

	testsuite.RunWebTests(t, rt, "testdata/handle.json")
}

func TestResend(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusHandled)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "how can we help", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, testdb.VonageChannel, testdb.Bob, "this failed", nil, models.MsgStatusFailed, false)
	catOut := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, testdb.VonageChannel, testdb.Cat, "no URN", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL WHERE id = $1`, catOut.ID)

	testsuite.RunWebTests(t, rt, "testdata/resend.json")
}

func TestBroadcast(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testdb.InsertOptIn(t, rt, testdb.Org1, "Polls")

	createRun := func(org *testdb.Org, contact *testdb.Contact, nodeUUID flows.NodeUUID) {
		sessionUUID := testdb.InsertFlowSession(t, rt, contact, models.FlowTypeMessaging, models.SessionStatusWaiting, nil, testdb.Favorites)
		testdb.InsertFlowRun(t, rt, org, sessionUUID, contact, testdb.Favorites, models.RunStatusWaiting, nodeUUID)
	}

	// put Bob and Cat in a flows at different nodes
	createRun(testdb.Org1, testdb.Bob, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdb.Org1, testdb.Cat, "a52a9e6d-34bb-4be1-8034-99e33d0862c6")

	testsuite.RunWebTests(t, rt, "testdata/broadcast.json")
}

func TestBroadcastPreview(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/broadcast_preview.json")
}
