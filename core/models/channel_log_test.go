package models_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/utils/svclogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelLogsOutgoing(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetDynamo)

	mocks := httpx.WithMocks(http.DefaultTransport, map[string][]*httpx.MockResponse{
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
	trace1, err := svclogs.TraceRequest(mocks, 0, req1)
	require.NoError(t, err)

	clog1.HTTP(trace1)
	clog1.End()

	req2, _ := httpx.NewRequest(ctx, "GET", "http://ivr.com/hangup", nil, nil)
	trace2, err := svclogs.TraceRequest(mocks, 0, req2)
	require.NoError(t, err)

	clog2.HTTP(trace2)
	clog2.Error(&svclogs.Error{Message: "oops"})
	clog2.End()

	_, err = rt.Dynamo.Main.Queue(clog1)
	require.NoError(t, err)
	_, err = rt.Dynamo.Main.Queue(clog2)
	require.NoError(t, err)

	rt.Dynamo.Main.Flush()

	dyntest.AssertCount(t, rt.Dynamo.Main.Client(), "TestMain", 2)

	// read log back from DynamoDB
	item, err := dynamo.GetItem(ctx, rt.Dynamo.Main.Client(), "TestMain", clog1.DynamoKey())
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
