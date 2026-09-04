package msg_test

import (
	"testing"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSend(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// add an unreachable contact (i.e. no URNs)
	testdb.InsertContact(t, rt, testdb.Org1, "f5e5c595-0cba-4eb9-b1e6-41d7f7f0add6", "Mr Unreachable", "eng", models.ContactStatusActive)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Date(2015, 1, 1, 12, 30, 45, 0, time.UTC), nil)

	testsuite.RunWebTests(t, rt, "testdata/send.json")

	testsuite.AssertCourierQueues(t, rt, map[string][]int{"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1, 1, 1, 1, 1}})
}

func TestDelete(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hello world", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "goodbye world", models.MsgStatusPending, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "stay visible", models.MsgStatusPending, "")

	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = NOW() WHERE id = $1`, testdb.Ann.ID)

	testsuite.IndexMessages(t, rt)

	msgs := testsuite.GetIndexedMessages(t, rt, false)
	require.Len(t, msgs, 3)

	testsuite.RunWebTests(t, rt, "testdata/delete.json")

	// first two messages should have been de-indexed, third should remain
	msgs = testsuite.GetIndexedMessages(t, rt, false)
	require.Len(t, msgs, 1)
	assert.Equal(t, "0199bad9-f0bc-7738-8af8-99712a6f8bff", msgs[0].ID)
}

func TestArchive(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "in my inbox", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "handled by a flow", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "not yet handled", models.MsgStatusPending, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.TwilioChannel, testdb.Ann, "already deleted", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org2, "0199bb09-f0e9-7489-a58e-69304a7941a0", testdb.Org2Channel, testdb.Org2Contact, "different org", models.MsgStatusHandled, "")

	// second message was handled by a flow so it's in the handled folder rather than the inbox
	rt.DB.MustExec(`UPDATE msgs_msg SET flow_id = $1, folder = 'W' WHERE uuid = '0199bad9-9791-770d-a47d-8f4a6ea3ad13'`, testdb.Favorites.ID)

	// fourth message has already been deleted by the user
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D', text = '' WHERE uuid = '0199bada-2b39-7cac-9714-827df9ec6b91'`)

	testsuite.RunWebTests(t, rt, "testdata/archive.json")
}

func TestRestore(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "back to my inbox", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "handled by a flow", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "not yet handled", models.MsgStatusPending, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.TwilioChannel, testdb.Ann, "never archived", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bb0a-4c2e-7a51-8f3d-1c6b5e9d0a72", testdb.TwilioChannel, testdb.Ann, "archived a long time ago", models.MsgStatusHandled, "")

	// second message was handled by a flow so restoring it should put it back in the handled folder
	rt.DB.MustExec(`UPDATE msgs_msg SET flow_id = $1 WHERE uuid = '0199bad9-9791-770d-a47d-8f4a6ea3ad13'`, testdb.Favorites.ID)

	// archive the two handled messages - the pending one can't be archived so stays where it is
	rt.DB.MustExec(`UPDATE msgs_msg SET folder = 'A'
	                 WHERE uuid IN ('0199bad8-f98d-75a3-b641-2718a25ac3f5', '0199bad9-9791-770d-a47d-8f4a6ea3ad13')`)

	// the last message was archived back when archiving still wrote the visibility, so it carries that value too
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'A', folder = 'A' WHERE uuid = '0199bb0a-4c2e-7a51-8f3d-1c6b5e9d0a72'`)

	testsuite.RunWebTests(t, rt, "testdata/restore.json")
}

func TestHandle(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusPending, "")
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "how can we help", nil, models.MsgStatusSent, false)

	testsuite.RunWebTests(t, rt, "testdata/handle.json")
}

func TestResend(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusHandled, "")
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "how can we help", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.VonageChannel, testdb.Bob, "this failed", nil, models.MsgStatusFailed, false)
	catOut := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.VonageChannel, testdb.Cat, "no URN", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL WHERE id = $1`, catOut.ID)

	testsuite.RunWebTests(t, rt, "testdata/resend.json")
}

func TestBroadcast(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	createRun := func(org *testdb.Org, contact *testdb.Contact, nodeUUID core.NodeUUID) {
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

	testsuite.IndexContacts(t, rt)

	testsuite.RunWebTests(t, rt, "testdata/broadcast_preview.json")
}

func TestSearch(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "01955193-de00-7000-8000-000000000001", testdb.TwilioChannel, testdb.Ann, "hello world", models.MsgStatusHandled, "")

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "019551ca-cc80-7000-8000-000000000002", testdb.TwilioChannel, testdb.Bob, "hello there friend", models.MsgStatusHandled, "019551ca-cc80-7000-8000-000000000099")

	testdb.InsertIncomingMsg(t, rt, testdb.Org1, "01955201-bb00-7000-8000-000000000003", testdb.TwilioChannel, testdb.Cat, "goodbye world", models.MsgStatusHandled, "")

	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = NOW() WHERE id IN ($1, $2, $3)`, testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID)

	testsuite.IndexMessages(t, rt)
	testsuite.WriteMessageHistory(t, rt)

	testsuite.RunWebTests(t, rt, "testdata/search.json")
}
