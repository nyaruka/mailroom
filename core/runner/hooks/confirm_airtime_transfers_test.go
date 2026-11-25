package hooks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
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

	rt.DB.MustExec(`UPDATE orgs_org SET config = '{"dtone_key": "key123", "dtone_secret": "sesame"}'::jsonb WHERE id = $1`, testdb.Org1.ID)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	seed := func() *models.AirtimeTransfer {
		testsuite.ClearDynamo(t, rt)
		rt.DB.MustExec(`DELETE FROM airtime_airtimetransfer`)
		uuid := events.NewEventUUID()
		tr := models.NewAirtimeTransfer(testdb.Org1.ID, testdb.Ann.ID, events.NewAirtimeCreated(uuid, &core.AirtimeTransfer{
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

	rowStatus := func(uuid events.EventUUID) models.AirtimeTransferStatus {
		var status models.AirtimeTransferStatus
		require.NoError(t, rt.DB.GetContext(ctx, &status, `SELECT status FROM airtime_airtimetransfer WHERE uuid = $1`, uuid))
		return status
	}

	confirmOK := `{"id":2237512891,"status":{"class":{"id":2,"message":"CONFIRMED"}}}`
	confirmErr := `{"errors":[{"code":1003001,"message":"Transaction not found"}]}`

	// the hook never mutates the row directly — it triggers Confirm and logs the HTTP calls. Status
	// transitions out of Created happen via the provider's status callback (handled in web/public).
	cases := []struct {
		name       string
		mocks      []*httpx.MockResponse
		expHTTP    int
		expIsError bool
	}{
		{
			name:       "200 success",
			mocks:      []*httpx.MockResponse{httpx.NewMockResponse(200, nil, []byte(confirmOK))},
			expHTTP:    1,
			expIsError: false,
		},
		{
			name:       "4xx provider rejection",
			mocks:      []*httpx.MockResponse{httpx.NewMockResponse(400, nil, []byte(confirmErr))},
			expHTTP:    1,
			expIsError: true,
		},
		{
			name:       "5xx retried then given up",
			mocks:      []*httpx.MockResponse{httpx.NewMockResponse(500, nil, []byte(confirmErr)), httpx.NewMockResponse(500, nil, []byte(confirmErr)), httpx.NewMockResponse(500, nil, []byte(confirmErr))},
			expHTTP:    1,
			expIsError: true,
		},
		{
			name:       "connection error",
			mocks:      []*httpx.MockResponse{httpx.MockConnectionError, httpx.MockConnectionError, httpx.MockConnectionError},
			expHTTP:    1,
			expIsError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt.HTTP.Services.Transport, _ = testsuite.MockTransport(map[string][]*httpx.MockResponse{
				"https://dvs-api.dtone.com/v1/async/transactions/2237512891/confirm": tc.mocks,
			})
			tr := seed()
			run(tr)

			// every branch leaves the row in Created — callback is the source of truth for transitions
			assert.Equal(t, models.AirtimeTransferStatusCreated, rowStatus(tr.UUID()))

			// the seeded transfer has no create calls so the only log is the confirm one
			rt.Dynamo.Main.Flush()
			items := dyntest.ScanAll(t, rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table())
			if assert.Len(t, items, 1) {
				assert.Equal(t, "att#"+string(tr.UUID()), items[0].PK)
				assert.Equal(t, "confirm", items[0].Data["type"])
				assert.Equal(t, tc.expIsError, items[0].Data["is_error"])

				data, err := items[0].GetData()
				require.NoError(t, err)
				assert.Len(t, data["http_logs"], tc.expHTTP)
			}
		})
	}
}
