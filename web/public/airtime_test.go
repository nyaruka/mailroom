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

	seed := func(t *testing.T) *models.AirtimeTransfer {
		rt.DB.MustExec(`DELETE FROM airtime_airtimetransfer`)
		tr := models.NewAirtimeTransfer(testdb.Org1.ID, testdb.Ann.ID, events.NewAirtimeCreated(&flows.AirtimeTransfer{
			ExternalID: "9876",
			Sender:     urns.URN("tel:+250700000001"),
			Recipient:  urns.URN("tel:+250700000002"),
			Currency:   "RWF",
			Amount:     decimal.RequireFromString("100"),
		}, nil))
		require.NoError(t, models.InsertAirtimeTransfers(ctx, rt.DB, []*models.AirtimeTransfer{tr}))
		return tr
	}

	post := func(t *testing.T, path, body string) int {
		req, err := http.NewRequest("POST", "http://localhost:8190"+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	rowStatus := func(t *testing.T) models.AirtimeTransferStatus {
		fetched, err := models.GetAirtimeTransferByExternalID(ctx, rt.DB, "9876")
		require.NoError(t, err)
		require.NotNil(t, fetched)
		return fetched.Status()
	}

	// happy path: P → S
	tr := seed(t)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":7,"message":"COMPLETED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusSuccess, rowStatus(t))

	// reversed after success is allowed (S → R)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":8,"message":"REVERSED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t))

	// late/out-of-order rejected after reversed must NOT walk the row back to F
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":3,"message":"REJECTED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t), "rejected after reversed should be ignored")

	// late completed after reversed must NOT walk the row back to S either
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":7,"message":"COMPLETED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t), "completed after reversed should be ignored")

	// rejected from pending takes the row to F directly
	tr = seed(t)
	_ = tr
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":3,"message":"REJECTED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusFailed, rowStatus(t))

	// declined and cancelled also map to F
	tr = seed(t)
	_ = tr
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":9,"message":"DECLINED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusFailed, rowStatus(t))

	tr = seed(t)
	_ = tr
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":4,"message":"CANCELLED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusFailed, rowStatus(t))

	// non-terminal status class is ignored — row stays in its current state
	seed(t)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":5,"message":"SUBMITTED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusPending, rowStatus(t))

	// duplicate of a terminal-equal status from the same callback: idempotent (P → S then redundant S → S)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":7,"message":"COMPLETED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusSuccess, rowStatus(t))
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", `{"id":9876,"status":{"class":{"id":7,"message":"COMPLETED"}}}`))
	assert.Equal(t, models.AirtimeTransferStatusSuccess, rowStatus(t))

	// unknown transaction id → 404 so DT One retries through the race window
	assert.Equal(t, http.StatusNotFound, post(t, "/mr/airtime/dtone/status/sek", `{"id":99999,"status":{"class":{"id":7,"message":"COMPLETED"}}}`))

	// wrong secret → 403
	assert.Equal(t, http.StatusForbidden, post(t, "/mr/airtime/dtone/status/wrong", `{"id":9876,"status":{"class":{"id":7,"message":"COMPLETED"}}}`))

	// missing transaction id → 400
	assert.Equal(t, http.StatusBadRequest, post(t, "/mr/airtime/dtone/status/sek", `{"status":{"class":{"id":7}}}`))
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
