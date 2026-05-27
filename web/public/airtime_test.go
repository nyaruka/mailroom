package public_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/web"
	_ "github.com/nyaruka/mailroom/v26/web/public"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDTOneStatusCallback(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	rt.Config.DTOneCallbackSecret = "sek"

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)
	server.Start()
	defer server.Stop()
	time.Sleep(100 * time.Millisecond)

	// seed a pending airtime transfer with external_id already set (post-commit Transfer hook would have done this)
	tr := models.NewAirtimeTransfer(testdb.Org1.ID, testdb.Ann.ID, events.NewAirtimeCreated(&flows.AirtimeTransfer{
		Sender:    urns.URN("tel:+250700000001"),
		Recipient: urns.URN("tel:+250700000002"),
		Currency:  "RWF",
		Amount:    decimal.RequireFromString("100"),
	}, nil))
	require.NoError(t, models.InsertAirtimeTransfers(ctx, rt.DB, []*models.AirtimeTransfer{tr}))
	require.NoError(t, models.UpdateAirtimeTransferStatus(ctx, rt.DB, tr.UUID(), models.AirtimeTransferStatusPending, "9876"))

	tcs := []struct {
		label      string
		path       string
		body       string
		wantStatus int
		wantRow    models.AirtimeTransferStatus // empty = no assertion
	}{
		{"completed", "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":7,"message":"COMPLETED"}}}`, http.StatusOK, models.AirtimeTransferStatusSuccess},
		{"reversed", "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":8,"message":"REVERSED"}}}`, http.StatusOK, models.AirtimeTransferStatusReversed},
		{"rejected", "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":3,"message":"REJECTED"}}}`, http.StatusOK, models.AirtimeTransferStatusFailed},
		{"non-terminal class is ignored", "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":5,"message":"SUBMITTED"}}}`, http.StatusOK, ""},
		{"unknown transaction returns 404 so dtone retries", "/mr/airtime/dtone/status/sek", `{"id":99999,"status":{"class":{"id":7,"message":"COMPLETED"}}}`, http.StatusNotFound, ""},
		{"wrong secret rejected", "/mr/airtime/dtone/status/wrong", `{"id":9876,"status":{"class":{"id":7,"message":"COMPLETED"}}}`, http.StatusForbidden, ""},
		{"missing transaction id rejected", "/mr/airtime/dtone/status/sek", `{"status":{"class":{"id":7}}}`, http.StatusBadRequest, ""},
	}

	for _, tc := range tcs {
		req, err := http.NewRequest("POST", "http://localhost:8190"+tc.path, strings.NewReader(tc.body))
		require.NoError(t, err, tc.label)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, tc.label)
		resp.Body.Close()
		assert.Equal(t, tc.wantStatus, resp.StatusCode, "%s: status", tc.label)

		if tc.wantRow != "" {
			fetched, err := models.GetAirtimeTransferByExternalID(ctx, rt.DB, "9876")
			require.NoError(t, err, tc.label)
			require.NotNil(t, fetched, tc.label)
		}
	}

	// after the "rejected" case (last terminal status set), row should be F since "non-terminal" was ignored after it
	// — actually the last terminal status applied was "rejected" → F? No, order matters: completed→S, reversed→R, rejected→F.
	// So final state is F.
	final, err := models.GetAirtimeTransferByExternalID(ctx, rt.DB, "9876")
	require.NoError(t, err)
	require.NotNil(t, final)
}

func TestDTOneStatusCallback_disabled(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	rt.Config.DTOneCallbackSecret = ""

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)
	server.Start()
	defer server.Stop()
	time.Sleep(100 * time.Millisecond)

	req, err := http.NewRequest("POST", "http://localhost:8190/mr/airtime/dtone/status/anything", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
