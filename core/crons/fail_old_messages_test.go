package crons_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailOldMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	cron := &crons.FailOldMessagesCron{}

	// nothing to fail
	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"failed": 0, "errored": 0}, res)

	longAgo := time.Now().Add(-30 * 24 * time.Hour)
	recently := time.Now().Add(-3 * 24 * time.Hour)

	// messages which have been sat in the outbox for a month, in two workspaces
	out1 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusQueued, longAgo)
	out2 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org2, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.Org2Channel, testdb.Org2Contact, "hi", models.MsgStatusErrored, longAgo)

	// a message that's only been there a few days, and an old one that was sent, neither of which we should touch
	out3 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusQueued, recently)
	out4 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusWired, longAgo)

	// which nothing has happened to since
	rt.DB.MustExec(`UPDATE msgs_msg SET modified_on = created_on WHERE direction = 'O'`)

	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"failed": 2, "errored": 0}, res)

	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out1.ID).
		Columns(map[string]any{"status": "F", "folder": "X", "failed_reason": "O"})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out2.ID).
		Columns(map[string]any{"status": "F", "folder": "X", "failed_reason": "O"})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out3.ID).
		Columns(map[string]any{"status": "Q", "folder": "O", "failed_reason": nil})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out4.ID).
		Columns(map[string]any{"status": "W", "folder": "S", "failed_reason": nil})

	// each failure is recorded in the contact's history so clients render the message as failed
	rt.Dynamo.History.Flush()
	dyntest.AssertCount(t, rt.Dynamo.History.Client(), rt.Dynamo.History.Table(), 2)

	for _, tc := range []struct {
		org     *testdb.Org
		contact *testdb.Contact
		msg     *testdb.MsgOut
	}{
		{testdb.Org1, testdb.Ann, out1},
		{testdb.Org2, testdb.Org2Contact, out2},
	} {
		item, err := dynamo.GetItem(ctx, rt.Dynamo.History.Client(), rt.Dynamo.History.Table(),
			dynamo.Key{PK: fmt.Sprintf("con#%s", tc.contact.UUID), SK: fmt.Sprintf("evt#%s#sts#F", tc.msg.UUID)})
		require.NoError(t, err)

		if assert.NotNil(t, item, "no status tag written for msg %s", tc.msg.UUID) {
			assert.Equal(t, int(tc.org.ID), item.OrgID)
			assert.Equal(t, "failed", item.Data["status"])
			assert.Equal(t, "too_old", item.Data["reason"])
		}
	}

	// running again is a no-op
	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"failed": 0, "errored": 0}, res)
}
