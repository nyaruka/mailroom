package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelLogsOutgoing(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	client, _ := testsuite.MockedHTTP(map[string][]*httpx.MockResponse{
		"http://ivr.com/start":  {httpx.NewMockResponse(200, nil, []byte("OK"))},
		"http://ivr.com/hangup": {httpx.NewMockResponse(400, nil, []byte("Oops"))},
	})

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	channel := oa.ChannelByID(testdb.TwilioChannel.ID)
	require.NotNil(t, channel)

	clog1 := models.NewChannelLog(models.ChannelLogTypeIVRStart, channel, []string{"sesame"})
	clog2 := models.NewChannelLog(models.ChannelLogTypeIVRHangup, channel, []string{"sesame"})

	req1, _ := httpx.NewRequest(ctx, "GET", "http://ivr.com/start", nil, map[string]string{"Authorization": "Token sesame"})
	trace1, _, err := utils.DoTraced(client, req1)
	require.NoError(t, err)

	clog1.HTTP(trace1)
	clog1.End()

	req2, _ := httpx.NewRequest(ctx, "GET", "http://ivr.com/hangup", nil, nil)
	trace2, _, err := utils.DoTraced(client, req2)
	require.NoError(t, err)

	clog2.HTTP(trace2)
	clog2.Error(&svclogs.Error{Message: "oops"})
	clog2.End()

	_, err = rt.Dynamo.Main.Queue(clog1)
	require.NoError(t, err)
	_, err = rt.Dynamo.Main.Queue(clog2)
	require.NoError(t, err)

	rt.Dynamo.Main.Flush()

	dyntest.AssertCount(t, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), 2)

	// read log back from DynamoDB
	item, err := dynamo.GetItem(ctx, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), clog1.DynamoKey())
	require.NoError(t, err)
	if assert.NotNil(t, item) {
		assert.Equal(t, string(models.ChannelLogTypeIVRStart), item.Data["type"])
		assert.Equal(t, clog1.CreatedOn.Truncate(time.Second).Add(time.Hour*24*7), *item.TTL)

		data, err := item.GetData()
		require.NoError(t, err)
		assert.Len(t, data["http_logs"], 1)

		assert.NotContains(t, string(jsonx.MustMarshal(data)), "sesame", "redacted value should not be present in DynamoDB log")
	}
}

func TestDeleteChannelLogsForMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	putLog := func(logUUID svclogs.UUID) {
		item := &dynamo.Item{
			Key:   models.ChannelLogDynamoKey(testdb.TwilioChannel.UUID, logUUID),
			OrgID: int(testdb.Org1.ID),
			Data:  map[string]any{"type": "msg_receive"},
		}
		require.NoError(t, dynamo.PutItem(ctx, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), item))
	}

	log1 := svclogs.UUID("0199c4cb-0000-7000-8000-000000000001")
	log2 := svclogs.UUID("0199c4cb-0000-7000-8000-000000000002")
	log3 := svclogs.UUID("0199c4cb-0000-7000-8000-000000000003")
	putLog(log1)
	putLog(log2)
	putLog(log3)

	msg1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199c4cb-f111-7ce8-9ce9-614d61a2c198", testdb.TwilioChannel, testdb.Ann, "hello world", models.MsgStatusHandled, "")
	msg2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199c4cf-486a-79af-9892-79254b6ac5b7", testdb.TwilioChannel, testdb.Ann, "goodbye world", models.MsgStatusHandled, "")
	msg3 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199c4d0-93b6-7420-a8dc-b53c94e2f1d3", testdb.TwilioChannel, testdb.Ann, "no logs", models.MsgStatusHandled, "")

	rt.DB.MustExec(`UPDATE msgs_msg SET log_uuids = ARRAY[$2, $3]::uuid[] WHERE id = $1`, msg1.ID, log1, log2)
	rt.DB.MustExec(`UPDATE msgs_msg SET log_uuids = ARRAY[$2]::uuid[] WHERE id = $1`, msg2.ID, log3)

	// deleting logs for msg1 and msg3 (no logs) should leave just msg2's log
	err := models.DeleteChannelLogsForMessages(ctx, rt, testdb.Org1.ID, []events.EventUUID{msg1.UUID, msg3.UUID})
	assert.NoError(t, err)

	rt.Dynamo.Main.Flush()

	dyntest.AssertCount(t, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), 1)

	item, err := dynamo.GetItem(ctx, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), models.ChannelLogDynamoKey(testdb.TwilioChannel.UUID, log3))
	require.NoError(t, err)
	assert.NotNil(t, item)
}
