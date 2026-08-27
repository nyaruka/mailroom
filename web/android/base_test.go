package android_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestEvent(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/event.json")
}

func TestMessage(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/message.json")
}

func TestStatus(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// 30000: queued, 30001: queued, 30002: queued, 30003: already sent, 30004: incoming, 30005: another workspace
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.AndroidChannel, testdb.Bob, "hi", nil, models.MsgStatusQueued, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.AndroidChannel, testdb.Cat, "hi", nil, models.MsgStatusQueued, false)
	sent := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusSent, false)
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bb09-f0e9-7489-a58e-69304a7941a0", testdb.AndroidChannel, testdb.Ann, "hi", models.MsgStatusHandled, "")
	testdb.InsertOutgoingMsg(t, rt, testdb.Org2, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.Org2Channel, testdb.Org2Contact, "hi", nil, models.MsgStatusQueued, false)

	// the queued messages have never been sent, but the sent one has a known sent_on
	rt.DB.MustExec(`UPDATE msgs_msg SET sent_on = '2025-05-04T12:30:45Z' WHERE id = $1`, sent.ID)

	testsuite.RunWebTests(t, rt, "testdata/status.json")
}

func TestSync(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{})

	testsuite.RunWebTests(t, rt, "testdata/sync.json")
}
