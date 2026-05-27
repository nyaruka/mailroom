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
	transfer := models.NewAirtimeTransfer(
		testdb.Org1.ID,
		testdb.Ann.ID,
		events.NewAirtimeCreated(&flows.AirtimeTransfer{
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

	// callback transitions pending → success
	updated, err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusPending, models.AirtimeTransferStatusSuccess)
	assert.NoError(t, err)
	assert.True(t, updated)

	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "S"})

	// can look it up by external_id
	fetched, err := models.GetAirtimeTransferByExternalID(ctx, rt.DB, "2237512891")
	assert.NoError(t, err)
	assert.NotNil(t, fetched)
	assert.Equal(t, transfer.UUID(), fetched.UUID())

	// missing external_id returns nil, nil
	fetched, err = models.GetAirtimeTransferByExternalID(ctx, rt.DB, "does-not-exist")
	assert.NoError(t, err)
	assert.Nil(t, fetched)

	// success → reversed is allowed (DT One can reverse after completion)
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusSuccess, models.AirtimeTransferStatusReversed)
	assert.NoError(t, err)
	assert.True(t, updated)

	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "R"})

	// reversed → success is NOT allowed — out-of-order callbacks don't walk the row backwards
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusReversed, models.AirtimeTransferStatusSuccess)
	assert.NoError(t, err)
	assert.False(t, updated)

	// reversed → failed is NOT allowed either
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusReversed, models.AirtimeTransferStatusFailed)
	assert.NoError(t, err)
	assert.False(t, updated)

	// caller-supplied current status that disagrees with the row is a no-op (someone else moved the row)
	updated, err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusPending, models.AirtimeTransferStatusSuccess)
	assert.NoError(t, err)
	assert.False(t, updated)

	// status remained reversed throughout
	assertdb.Query(t, rt.DB, `SELECT status FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "R"})
}
