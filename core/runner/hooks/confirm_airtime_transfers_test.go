package hooks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covers the post-commit Confirm hook's failure branches that the runner-level handler test can't
// exercise cleanly because they want different mock responses per case.
func TestConfirmAirtimeTransfers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)
	defer httpx.SetRequestor(httpx.DefaultRequestor)

	rt.DB.MustExec(`UPDATE orgs_org SET config = '{"dtone_key": "key123", "dtone_secret": "sesame"}'::jsonb WHERE id = $1`, testdb.Org1.ID)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	seed := func(externalID string) *models.AirtimeTransfer {
		rt.DB.MustExec(`DELETE FROM request_logs_httplog WHERE airtime_transfer_id IS NOT NULL`)
		rt.DB.MustExec(`DELETE FROM airtime_airtimetransfer`)
		uuid := flows.NewEventUUID()
		tr := models.NewAirtimeTransfer(testdb.Org1.ID, testdb.Ann.ID, events.NewAirtimeCreated(uuid, &flows.AirtimeTransfer{
			ExternalID: externalID,
			Sender:     urns.URN("tel:+250700000001"),
			Recipient:  urns.URN("tel:+250700000002"),
			Currency:   "RWF",
			Amount:     decimal.RequireFromString("100"),
		}, nil))
		require.NoError(t, models.InsertAirtimeTransfers(ctx, rt.DB, []*models.AirtimeTransfer{tr}))
		return tr
	}

	run := func(transfer *models.AirtimeTransfer) {
		mc, contact, _ := testdb.Ann.Load(t, rt, oa)
		scene := runner.NewScene(mc, contact)
		scenes := map[*runner.Scene][]any{scene: {transfer}}
		require.NoError(t, hooks.ConfirmAirtimeTransfers.Execute(ctx, rt, oa, scenes))
	}

	rowStatus := func(uuid flows.EventUUID) models.AirtimeTransferStatus {
		fetched, err := models.GetAirtimeTransferByUUID(ctx, rt.DB, uuid)
		require.NoError(t, err)
		require.NotNil(t, fetched)
		return fetched.Status()
	}

	confirmOK := `{"id":2237512891,"status":{"class":{"id":2,"message":"CONFIRMED"}}}`
	confirmErr := `{"errors":[{"code":1003001,"message":"Transaction not found"}]}`

	// 200 success → row stays pending (terminal status flows in via callback later)
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": {
			httpx.NewMockResponse(200, nil, []byte(confirmOK)),
		},
	}))
	tr := seed("2237512891")
	run(tr)
	assert.Equal(t, models.AirtimeTransferStatusPending, rowStatus(tr.UUID()), "200 OK leaves row pending")
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM request_logs_httplog WHERE airtime_transfer_id = $1`, tr.ID()).Returns(1)

	// 4xx permanent error → row flipped to failed
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": {
			httpx.NewMockResponse(400, nil, []byte(confirmErr)),
		},
	}))
	tr = seed("2237512891")
	run(tr)
	assert.Equal(t, models.AirtimeTransferStatusFailed, rowStatus(tr.UUID()), "4xx confirm marks row failed")

	// 5xx transient error → row stays pending (provider's auto-cancel callback will eventually arrive)
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": {
			// the client retries on 5xx; mock enough responses to exhaust retries
			httpx.NewMockResponse(500, nil, []byte(confirmErr)),
			httpx.NewMockResponse(500, nil, []byte(confirmErr)),
			httpx.NewMockResponse(500, nil, []byte(confirmErr)),
		},
	}))
	tr = seed("2237512891")
	run(tr)
	assert.Equal(t, models.AirtimeTransferStatusPending, rowStatus(tr.UUID()), "5xx confirm leaves row pending for provider auto-cancel")

	// connection error → row stays pending
	httpx.SetRequestor(httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": {
			httpx.MockConnectionError, httpx.MockConnectionError, httpx.MockConnectionError,
		},
	}))
	tr = seed("2237512891")
	run(tr)
	assert.Equal(t, models.AirtimeTransferStatusPending, rowStatus(tr.UUID()), "connection error leaves row pending")
}
