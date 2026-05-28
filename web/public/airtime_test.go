package public_test

import (
	"fmt"
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

	seed := func(t *testing.T) flows.EventUUID {
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
		return uuid
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

	rowStatus := func(t *testing.T, uuid flows.EventUUID) models.AirtimeTransferStatus {
		fetched, err := models.GetAirtimeTransferByUUID(ctx, rt.DB, uuid)
		require.NoError(t, err)
		require.NotNil(t, fetched)
		return fetched.Status()
	}

	// body builder for a callback whose external_id field carries our row UUID
	body := func(uuid flows.EventUUID, classID int, msg string) string {
		return fmt.Sprintf(`{"id":2237512891,"external_id":%q,"status":{"class":{"id":%d,"message":%q}}}`, string(uuid), classID, msg)
	}

	// happy path: P → S
	uuid := seed(t)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 7, "COMPLETED")))
	assert.Equal(t, models.AirtimeTransferStatusSuccess, rowStatus(t, uuid))

	// reversed after success is allowed (S → R)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 8, "REVERSED")))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t, uuid))

	// late/out-of-order rejected after reversed must NOT walk the row back to F
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 3, "REJECTED")))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t, uuid), "rejected after reversed should be ignored")

	// late completed after reversed must NOT walk the row back to S either
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 7, "COMPLETED")))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t, uuid), "completed after reversed should be ignored")

	// rejected from pending takes the row to F directly
	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 3, "REJECTED")))
	assert.Equal(t, models.AirtimeTransferStatusFailed, rowStatus(t, uuid))

	// declined and cancelled also map to F
	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 9, "DECLINED")))
	assert.Equal(t, models.AirtimeTransferStatusFailed, rowStatus(t, uuid))

	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 4, "CANCELLED")))
	assert.Equal(t, models.AirtimeTransferStatusFailed, rowStatus(t, uuid))

	// non-terminal status class is ignored — row stays in its current state
	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 5, "SUBMITTED")))
	assert.Equal(t, models.AirtimeTransferStatusPending, rowStatus(t, uuid))

	// same terminal callback delivered twice is idempotent
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 7, "COMPLETED")))
	assert.Equal(t, models.AirtimeTransferStatusSuccess, rowStatus(t, uuid))
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(uuid, 7, "COMPLETED")))
	assert.Equal(t, models.AirtimeTransferStatusSuccess, rowStatus(t, uuid))

	// unknown UUID → 200 ignored (the compare-and-swap is indistinguishable from a no-op transition; we
	// don't pay an extra SELECT just to give 404, and the response also doubles as an anti-enumeration
	// shield since attackers can't distinguish "this UUID isn't ours" from "this transition isn't allowed")
	assert.Equal(t, http.StatusOK, post(t, "/mr/airtime/dtone/status/sek", body(flows.NewEventUUID(), 7, "COMPLETED")))

	// wrong secret → 403
	assert.Equal(t, http.StatusForbidden, post(t, "/mr/airtime/dtone/status/wrong", body(uuid, 7, "COMPLETED")))

	// missing external_id → 400
	assert.Equal(t, http.StatusBadRequest, post(t, "/mr/airtime/dtone/status/sek", `{"id":1,"status":{"class":{"id":7}}}`))
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
