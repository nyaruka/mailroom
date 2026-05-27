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

	assertdb.Query(t, rt.DB, `SELECT org_id, status, external_id from airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"org_id": 1, "status": "P", "external_id": "2237512891"})

	// callback transitions pending → success
	err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusSuccess, "")
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status, external_id FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "S", "external_id": "2237512891"})

	// can look it up by external_id
	fetched, err := models.GetAirtimeTransferByExternalID(ctx, rt.DB, "2237512891")
	assert.NoError(t, err)
	assert.NotNil(t, fetched)
	assert.Equal(t, transfer.UUID(), fetched.UUID())

	// missing external_id returns nil, nil
	fetched, err = models.GetAirtimeTransferByExternalID(ctx, rt.DB, "does-not-exist")
	assert.NoError(t, err)
	assert.Nil(t, fetched)

	// later callback marks it reversed — status updates, external_id is preserved
	err = models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusReversed, "")
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status, external_id FROM airtime_airtimetransfer WHERE id = $1`, transfer.ID()).
		Columns(map[string]any{"status": "R", "external_id": "2237512891"})
}
