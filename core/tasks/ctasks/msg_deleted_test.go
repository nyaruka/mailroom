package ctasks_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMsgDeleted(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(t, rt)

	ann, _, _ := testdb.Ann.Load(t, rt, oa)

	msg1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199c4cb-f111-7ce8-9ce9-614d61a2c198", testdb.TwilioChannel, testdb.Ann, "hello world", models.MsgStatusHandled, "")
	msg2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199c4cf-486a-79af-9892-79254b6ac5b7", testdb.TwilioChannel, testdb.Ann, "goodbye world", models.MsgStatusHandled, "")

	// give each message a channel log in DynamoDB
	putLog := func(msg *testdb.MsgIn, logUUID svclogs.UUID) {
		item := &dynamo.Item{
			Key:   models.ChannelLogDynamoKey(testdb.TwilioChannel.UUID, logUUID),
			OrgID: int(testdb.Org1.ID),
			Data:  map[string]any{"type": "msg_receive"},
		}
		require.NoError(t, dynamo.PutItem(ctx, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), item))
		rt.DB.MustExec(`UPDATE msgs_msg SET log_uuids = ARRAY[$2]::uuid[] WHERE id = $1`, msg.ID, logUUID)
	}
	putLog(msg1, "0199c4cb-0000-7000-8000-000000000001")
	putLog(msg2, "0199c4cb-0000-7000-8000-000000000002")

	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = NOW() WHERE id = $1`, testdb.Ann.ID)

	testsuite.IndexMessages(t, rt)

	msgs := testsuite.GetIndexedMessages(t, rt, false)
	assert.Len(t, msgs, 2)

	task := &ctasks.MsgDeleted{
		MsgUUID: "0199c4cb-f111-7ce8-9ce9-614d61a2c198",
	}

	err := task.Perform(ctx, rt, oa, ann)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT uuid::text, visibility FROM msgs_msg`).Map(map[string]any{
		"0199c4cb-f111-7ce8-9ce9-614d61a2c198": "X",
		"0199c4cf-486a-79af-9892-79254b6ac5b7": "V",
	})

	// deleted message should be de-indexed, other should remain
	msgs = testsuite.GetIndexedMessages(t, rt, false)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "0199c4cf-486a-79af-9892-79254b6ac5b7", msgs[0].ID)

	// deleted message's channel log should be deleted from DynamoDB, other should remain
	rt.Dynamo.Main.Flush()

	item, err := dynamo.GetItem(ctx, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), models.ChannelLogDynamoKey(testdb.TwilioChannel.UUID, "0199c4cb-0000-7000-8000-000000000001"))
	require.NoError(t, err)
	assert.Nil(t, item)

	item, err = dynamo.GetItem(ctx, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), models.ChannelLogDynamoKey(testdb.TwilioChannel.UUID, "0199c4cb-0000-7000-8000-000000000002"))
	require.NoError(t, err)
	assert.NotNil(t, item)

	items := testsuite.GetHistoryItems(t, rt, false, time.Time{})
	if assert.Equal(t, 2, len(items)) {
		assert.Equal(t, "con#a393abc0-283d-4c9b-a1b3-641a035c34bf", items[0].PK)
		assert.Equal(t, "evt#0199c4cb-f111-7ce8-9ce9-614d61a2c198#del", items[0].SK)

		data, err := items[0].GetData()
		require.NoError(t, err)
		assert.Equal(t, true, data["by_contact"])

		assert.Equal(t, "con#a393abc0-283d-4c9b-a1b3-641a035c34bf", items[1].PK)
		assert.Regexp(t, "evt#[a-z0-9\\-]{36}", items[1].SK)

		data, err = items[1].GetData()
		require.NoError(t, err)
		assert.Equal(t, "msg_deleted", data["type"])
	}
}
