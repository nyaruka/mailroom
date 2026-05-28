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

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)
	server.Start()
	defer server.Stop()
	time.Sleep(100 * time.Millisecond)

	const callbackPath = "/mr/airtime/dtone/status"

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

	post := func(t *testing.T, body string) int {
		req, err := http.NewRequest("POST", "http://localhost:8190"+callbackPath, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	rowStatus := func(t *testing.T, uuid flows.EventUUID) models.AirtimeTransferStatus {
		var status models.AirtimeTransferStatus
		require.NoError(t, rt.DB.GetContext(ctx, &status, `SELECT status FROM airtime_airtimetransfer WHERE uuid = $1`, uuid))
		return status
	}

	// body builder for a callback whose external_id field carries our row UUID
	body := func(uuid flows.EventUUID, classID int, msg string) string {
		return fmt.Sprintf(`{"id":2237512891,"external_id":%q,"status":{"class":{"id":%d,"message":%q}}}`, string(uuid), classID, msg)
	}

	// start from a clean history table so we can assert exactly what each callback writes
	testsuite.GetHistoryItems(t, rt, true, time.Time{})

	// happy path through the lifecycle: P → C → B → S → R
	uuid := seed(t)
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 2, "CONFIRMED")))
	assert.Equal(t, models.AirtimeTransferStatusConfirmed, rowStatus(t, uuid))
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 5, "SUBMITTED")))
	assert.Equal(t, models.AirtimeTransferStatusSubmitted, rowStatus(t, uuid))
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 7, "COMPLETED")))
	assert.Equal(t, models.AirtimeTransferStatusCompleted, rowStatus(t, uuid))
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 8, "REVERSED")))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t, uuid))

	// each successful transition is recorded as an `sts` event tag keyed by the transfer's UUID; the
	// tags share a sort key so the latest callback overwrites, leaving one tag at the current status
	items := testsuite.GetHistoryItems(t, rt, true, time.Time{})
	if assert.Len(t, items, 1) {
		assert.Equal(t, fmt.Sprintf("con#%s", testdb.Ann.UUID), items[0].PK)
		assert.Equal(t, fmt.Sprintf("evt#%s#sts", uuid), items[0].SK)
		data, err := items[0].GetData()
		require.NoError(t, err)
		assert.Equal(t, "reversed", data["status"])
		assert.NotEmpty(t, data["created_on"])
	}

	// each terminal failure class maps to its own status, and writes its own status tag
	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 3, "REJECTED")))
	assert.Equal(t, models.AirtimeTransferStatusRejected, rowStatus(t, uuid))

	items = testsuite.GetHistoryItems(t, rt, true, time.Time{})
	if assert.Len(t, items, 1) {
		assert.Equal(t, fmt.Sprintf("evt#%s#sts", uuid), items[0].SK)
		data, err := items[0].GetData()
		require.NoError(t, err)
		assert.Equal(t, "rejected", data["status"])
	}

	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 9, "DECLINED")))
	assert.Equal(t, models.AirtimeTransferStatusDeclined, rowStatus(t, uuid))

	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 4, "CANCELLED")))
	assert.Equal(t, models.AirtimeTransferStatusCancelled, rowStatus(t, uuid))

	// CREATED status class (1) on a callback is ignored — that's just the initial state we set the row
	// to — and an ignored callback writes nothing to history
	testsuite.GetHistoryItems(t, rt, true, time.Time{}) // clear prior writes
	uuid = seed(t)
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 1, "CREATED")))
	assert.Equal(t, models.AirtimeTransferStatusCreated, rowStatus(t, uuid))
	assert.Empty(t, testsuite.GetHistoryItems(t, rt, true, time.Time{}), "ignored CREATED-class callback should not write history")

	// a Reversed callback that arrives without a preceding Completed still applies — better to record
	// the eventual reversal than silently drop it because the lifecycle skipped a stage
	assert.Equal(t, http.StatusOK, post(t, body(uuid, 8, "REVERSED")))
	assert.Equal(t, models.AirtimeTransferStatusReversed, rowStatus(t, uuid))

	// unknown UUID → 200 ignored (the CAS finds no row to update; the distinction isn't actionable
	// for DT One and the matching response shape doubles as an anti-enumeration shield), and writes
	// nothing to history
	testsuite.GetHistoryItems(t, rt, true, time.Time{}) // clear prior writes
	assert.Equal(t, http.StatusOK, post(t, body(flows.NewEventUUID(), 7, "COMPLETED")))
	assert.Empty(t, testsuite.GetHistoryItems(t, rt, true, time.Time{}), "ignored unknown-UUID callback should not write history")

	// a forged callback with a real UUID but wrong DT One tx id is a no-op (defense in depth — the
	// matching CAS requires both halves)
	uuid = seed(t)
	mismatched := fmt.Sprintf(`{"id":99999999,"external_id":%q,"status":{"class":{"id":7,"message":"COMPLETED"}}}`, string(uuid))
	assert.Equal(t, http.StatusOK, post(t, mismatched))
	assert.Equal(t, models.AirtimeTransferStatusCreated, rowStatus(t, uuid), "wrong tx id should not mutate the row")

	// missing external_id → 400
	assert.Equal(t, http.StatusBadRequest, post(t, `{"id":1,"status":{"class":{"id":7}}}`))

	// missing id → 400
	assert.Equal(t, http.StatusBadRequest, post(t, fmt.Sprintf(`{"external_id":%q,"status":{"class":{"id":7}}}`, string(uuid))))
}
