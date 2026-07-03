package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestAirtimeTransfers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer rt.DB.MustExec(`DELETE FROM airtime_airtimetransfer`)

	// insert a transfer — new transfers are always created in pending state, with the provider's
	// transaction id already populated from the event
	transferUUID := events.NewEventUUID()
	transfer := models.NewAirtimeTransfer(
		testdb.Org1.ID,
		testdb.Ann.ID,
		events.NewAirtimeCreated(transferUUID, &core.AirtimeTransfer{
			ExternalID: "2237512891",
			Sender:     urns.URN("tel:+250700000001"),
			Recipient:  urns.URN("tel:+250700000002"),
			Currency:   "RWF",
			Amount:     decimal.RequireFromString(`100`),
		}, nil),
	)
	err := models.InsertAirtimeTransfers(ctx, rt.DB, []*models.AirtimeTransfer{transfer})
	assert.NoError(t, err)
	assert.NotEqual(t, models.NilAirtimeTransferID, transfer.ID())
	assert.Equal(t, models.AirtimeTransferStatusCreated, transfer.Status())

	assertdb.Query(t, rt.DB, `SELECT org_id, status, external_id from airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"org_id": 1, "status": "P", "external_id": "2237512891"})

	// callback whose provider tx id doesn't match the row's external_id is a no-op (defense in depth —
	// a forged callback would have to know both the UUID and DT One's id to mutate the row)
	tag, err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "wrong-tx-id", models.AirtimeTransferStatusCompleted)
	assert.NoError(t, err)
	assert.Nil(t, tag, "no row should be updated when the provider tx id doesn't match")

	// callback transitions pending → success when both UUID and provider tx id line up, returning an
	// event tag that records the change for the contact's history
	tag, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "2237512891", models.AirtimeTransferStatusCompleted)
	assert.NoError(t, err)
	if assert.NotNil(t, tag) {
		assert.Equal(t, testdb.Org1.ID, tag.OrgID)
		assert.Equal(t, testdb.Ann.UUID, tag.ContactUUID)
		assert.Equal(t, transfer.UUID(), tag.EventUUID)
		assert.Equal(t, "sts", tag.Tag)
		assert.Equal(t, "completed", tag.Data["status"])
	}

	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "S"})

	// any status update with matching uuid + external_id is applied — no transition guard, since
	// dropping out-of-order callbacks could strand the row (e.g. a Reversed callback for a transfer
	// we never saw Completed for)
	tag, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "2237512891", models.AirtimeTransferStatusReversed)
	assert.NoError(t, err)
	if assert.NotNil(t, tag) {
		assert.Equal(t, "reversed", tag.Data["status"])
	}

	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "R"})
}

func TestNewAirtimeStatusTag(t *testing.T) {
	// every status must map to a non-empty external name (consumed as the event's _status by clients) —
	// an unmapped status would silently write `"status": ""` and render as an empty badge with no error.
	// Any new AirtimeTransferStatus constant must be added here.
	names := map[models.AirtimeTransferStatus]string{
		models.AirtimeTransferStatusCreated:   "created",
		models.AirtimeTransferStatusConfirmed: "confirmed",
		models.AirtimeTransferStatusSubmitted: "submitted",
		models.AirtimeTransferStatusCompleted: "completed",
		models.AirtimeTransferStatusReversed:  "reversed",
		models.AirtimeTransferStatusRejected:  "rejected",
		models.AirtimeTransferStatusCancelled: "cancelled",
		models.AirtimeTransferStatusDeclined:  "declined",
	}

	for status, name := range names {
		tag := models.NewAirtimeStatusTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", status)
		assert.Equal(t, testdb.Org1.ID, tag.OrgID)
		assert.Equal(t, testdb.Ann.UUID, tag.ContactUUID)
		assert.Equal(t, events.EventUUID("0197b335-6ded-79a4-95a6-3af85b57f108"), tag.EventUUID)
		assert.Equal(t, "sts", tag.Tag)
		assert.Equal(t, name, tag.Data["status"], "unexpected name for status %q", status)
		assert.NotEmpty(t, tag.Data["status"], "status %q has no external name", status)
	}
}
