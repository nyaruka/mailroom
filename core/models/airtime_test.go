package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
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
	transferUUID := flows.NewEventUUID()
	transfer := models.NewAirtimeTransfer(
		testdb.Org1.ID,
		testdb.Ann.ID,
		events.NewAirtimeCreated(transferUUID, &flows.AirtimeTransfer{
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
	assert.Equal(t, models.AirtimeTransferStatusPending, transfer.Status())

	assertdb.Query(t, rt.DB, `SELECT org_id, status, external_id from airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"org_id": 1, "status": "P", "external_id": "2237512891"})

	// callback whose provider tx id doesn't match the row's external_id is a no-op (defense in depth —
	// a forged callback would have to know both the UUID and DT One's id to mutate the row)
	updated, err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "wrong-tx-id", models.AirtimeTransferStatusSuccess)
	assert.NoError(t, err)
	assert.False(t, updated)

	// callback transitions pending → success when both UUID and provider tx id line up
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "2237512891", models.AirtimeTransferStatusSuccess)
	assert.NoError(t, err)
	assert.True(t, updated)

	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "S"})

	// success → reversed is allowed (DT One can reverse after completion)
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "2237512891", models.AirtimeTransferStatusReversed)
	assert.NoError(t, err)
	assert.True(t, updated)

	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "R"})

	// reversed → success is NOT allowed — out-of-order callbacks don't walk the row backwards
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "2237512891", models.AirtimeTransferStatusSuccess)
	assert.NoError(t, err)
	assert.False(t, updated)

	// reversed → failed is NOT allowed either
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), "2237512891", models.AirtimeTransferStatusFailed)
	assert.NoError(t, err)
	assert.False(t, updated)

	// status remained reversed throughout
	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "R"})
}
