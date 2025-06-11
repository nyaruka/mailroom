package models_test

import (
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/clogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelLogsOutgoing(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetDynamo)

	defer httpx.SetRequestor(httpx.DefaultRequestor)
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"http://ivr.com/start":  {httpx.NewMockResponse(200, nil, []byte("OK"))},
		"http://ivr.com/hangup": {httpx.NewMockResponse(400, nil, []byte("Oops"))},
	}))

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	channel := oa.ChannelByID(testdb.TwilioChannel.ID)
	require.NotNil(t, channel)

	clog1 := models.NewChannelLog(models.ChannelLogTypeIVRStart, channel, []string{"sesame"})
	clog2 := models.NewChannelLog(models.ChannelLogTypeIVRHangup, channel, []string{"sesame"})

	req1, _ := httpx.NewRequest(ctx, "GET", "http://ivr.com/start", nil, map[string]string{"Authorization": "Token sesame"})
	trace1, err := httpx.DoTrace(http.DefaultClient, req1, nil, nil, -1)
	require.NoError(t, err)

	clog1.HTTP(trace1)
	clog1.End()

	req2, _ := httpx.NewRequest(ctx, "GET", "http://ivr.com/hangup", nil, nil)
	trace2, err := httpx.DoTrace(http.DefaultClient, req2, nil, nil, -1)
	require.NoError(t, err)

	clog2.HTTP(trace2)
	clog2.Error(&clogs.Error{Message: "oops"})
	clog2.End()

	err = models.InsertChannelLogs(ctx, rt, []*models.ChannelLog{clog1, clog2})
	require.NoError(t, err)

	count, err := rt.Dynamo.Main.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// read log back from DynamoDB
	item, err := rt.Dynamo.Main.GetItem(ctx, clog1.DynamoKey())
	require.NoError(t, err)
	assert.Equal(t, string(models.ChannelLogTypeIVRStart), item.Data["type"])

	var dataGZ map[string]any
	err = dynamo.UnmarshalJSONGZ(item.DataGZ, &dataGZ)
	require.NoError(t, err)
	assert.Len(t, dataGZ["http_logs"], 1)

	assert.NotContains(t, string(item.DataGZ), "sesame", "redacted value should not be present in DynamoDB log")
}
