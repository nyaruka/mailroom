package hooks_test

import (
	"net/http"
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
	rt.Config.Domain = "mailroom.example.com"

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	rt.DB.MustExec(`UPDATE orgs_org SET config = '{"dtone_key": "key123", "dtone_secret": "sesame"}'::jsonb WHERE id = $1`, testdb.Org1.ID)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	seed := func() *models.AirtimeTransfer {
		rt.DB.MustExec(`DELETE FROM request_logs_httplog WHERE airtime_transfer_id IS NOT NULL`)
		rt.DB.MustExec(`DELETE FROM airtime_airtimetransfer`)
		uuid := flows.NewEventUUID()
		tr := models.NewAirtimeTransfer(testdb.Org1.ID, testdb.Ann.ID, events.NewAirtimeCreated(uuid, &flows.AirtimeTransfer{
			ExternalID: "2237512891",
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
		var status models.AirtimeTransferStatus
		require.NoError(t, rt.DB.GetContext(ctx, &status, `SELECT status FROM airtime_airtimetransfer WHERE uuid = $1`, uuid))
		return status
	}

	confirmOK := `{"id":2237512891,"status":{"class":{"id":2,"message":"CONFIRMED"}}}`
	confirmErr := `{"errors":[{"code":1003001,"message":"Transaction not found"}]}`

	// the hook never mutates the row directly — it triggers Confirm and attaches HTTP logs. Status
	// transitions out of Created happen via the provider's status callback (handled in web/public).
	cases := []struct {
		name    string
		mocks   []*httpx.MockResponse
		expLogs int
	}{
		{
			name:    "200 success",
			mocks:   []*httpx.MockResponse{httpx.NewMockResponse(200, nil, []byte(confirmOK))},
			expLogs: 1,
		},
		{
			name:    "4xx provider rejection",
			mocks:   []*httpx.MockResponse{httpx.NewMockResponse(400, nil, []byte(confirmErr))},
			expLogs: 1,
		},
		{
			name:    "5xx retried then given up",
			mocks:   []*httpx.MockResponse{httpx.NewMockResponse(500, nil, []byte(confirmErr)), httpx.NewMockResponse(500, nil, []byte(confirmErr)), httpx.NewMockResponse(500, nil, []byte(confirmErr))},
			expLogs: 1,
		},
		{
			name:    "connection error",
			mocks:   []*httpx.MockResponse{httpx.MockConnectionError, httpx.MockConnectionError, httpx.MockConnectionError},
			expLogs: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt.HTTP.Services.Transport = httpx.WithMocks(http.DefaultTransport, map[string][]*httpx.MockResponse{
				"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": tc.mocks,
			})
			tr := seed()
			run(tr)

			// every branch leaves the row in Created — callback is the source of truth for transitions
			assert.Equal(t, models.AirtimeTransferStatusCreated, rowStatus(tr.UUID()))
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM request_logs_httplog WHERE airtime_transfer_id = $1`, tr.ID()).Returns(tc.expLogs)
		})
	}
}
