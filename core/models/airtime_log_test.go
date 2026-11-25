package models_test

import (
	"errors"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAirtimeLog(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	transferUUID := events.EventUUID("01969b47-47eb-76f8-b3a5-b9dac4767740")
	t1 := time.Date(2025, 5, 4, 12, 30, 0, 0, time.UTC)

	httpLog := func(url string, code int, status core.CallStatus, elapsedMS int, createdOn time.Time) *core.HTTPLog {
		return &core.HTTPLog{
			HTTPLogWithoutTime: &core.HTTPLogWithoutTime{
				LogWithoutTime: &httpx.LogWithoutTime{URL: url, StatusCode: code, Request: "GET / HTTP/1.1", ElapsedMS: elapsedMS},
				Status:         status,
			},
			CreatedOn: createdOn,
		}
	}

	log1 := models.NewAirtimeLog(models.AirtimeLogTypeCreate, testdb.Org1.ID, transferUUID, []*core.HTTPLog{
		httpLog("https://dvs-api.dtone.com/v1/lookup", 200, core.CallStatusSuccess, 150, t1),
		httpLog("https://dvs-api.dtone.com/v1/async/transactions", 201, core.CallStatusSuccess, 250, t1.Add(time.Second)),
	}, nil)

	assert.Equal(t, t1, log1.CreatedOn)
	assert.Equal(t, 400*time.Millisecond, log1.Elapsed)
	assert.Len(t, log1.HttpLogs, 2)
	assert.False(t, log1.IsError())
	assert.Equal(t, "att#01969b47-47eb-76f8-b3a5-b9dac4767740", log1.DynamoKey().PK)
	assert.Equal(t, "log#"+string(log1.UUID), log1.DynamoKey().SK)

	log2 := models.NewAirtimeLog(models.AirtimeLogTypeConfirm, testdb.Org1.ID, transferUUID, []*core.HTTPLog{
		httpLog("https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm", 400, core.CallStatusResponseError, 100, t1.Add(time.Minute)),
	}, errors.New("transaction not found"))

	assert.True(t, log2.IsError())

	_, err := rt.Dynamo.Main.Queue(log1)
	require.NoError(t, err)
	_, err = rt.Dynamo.Main.Queue(log2)
	require.NoError(t, err)
	rt.Dynamo.Main.Flush()

	item, err := dynamo.GetItem(ctx, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), log2.DynamoKey())
	require.NoError(t, err)
	if assert.NotNil(t, item) {
		assert.Equal(t, int(testdb.Org1.ID), item.OrgID)
		assert.Equal(t, "confirm", item.Data["type"])
		assert.Equal(t, true, item.Data["is_error"])
		assert.Equal(t, t1.Add(time.Minute).Add(time.Hour*24*7), item.TTL.UTC())

		data, err := item.GetData()
		require.NoError(t, err)
		assert.Len(t, data["http_logs"], 1)
		assert.Equal(t, []any{map[string]any{"code": "", "message": "transaction not found"}}, data["errors"])
	}
}
