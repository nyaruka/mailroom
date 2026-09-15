package msg_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestLabel(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "no labels yet", models.MsgStatusHandled, "")
	labelled := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "already labelled", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "not in the request", models.MsgStatusHandled, "")
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "outgoing", nil, models.MsgStatusSent, false)
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.TwilioChannel, testdb.Ann, "already deleted", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org2, "0199bb09-f0e9-7489-a58e-69304a7941a0", testdb.Org2Channel, testdb.Org2Contact, "different org", models.MsgStatusHandled, "")

	labelled.Label(rt, testdb.TestingLabel)

	// fourth message has been deleted by the user so can't be labelled
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D', text = '' WHERE uuid = '0199bada-2b39-7cac-9714-827df9ec6b91'`)

	// backdate modified_on on everything so we can see which messages each request touches
	rt.DB.MustExec(`UPDATE msgs_msg SET modified_on = '2020-01-01 00:00:00+00'`)

	testsuite.RunWebTests(t, rt, "testdata/label.json")
}
