package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestAirtimeTransfers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer rt.DB.MustExec(`DELETE FROM airtime_airtimetransfer`)

	// insert a transfer
	transfer := models.NewAirtimeTransfer(
		testdb.Org1.ID,
		testdb.Ann.ID,
		events.NewAirtimeTransferred(&flows.AirtimeTransfer{
			ExternalID: "2237512891",
			Sender:     urns.URN("tel:+250700000001"),
			Recipient:  urns.URN("tel:+250700000002"),
			Currency:   "RWF",
			Amount:     decimal.RequireFromString(`100`),
		}, nil),
	)
	err := models.InsertAirtimeTransfers(ctx, rt.DB, []*models.AirtimeTransfer{transfer})
	assert.Nil(t, err)

	assertdb.Query(t, rt.DB, `SELECT org_id, status, external_id from airtime_airtimetransfer`).Columns(map[string]any{"org_id": int64(1), "status": "S", "external_id": "2237512891"})

	// insert a failed transfer with nil sender, empty currency
	transfer = models.NewAirtimeTransfer(
		testdb.Org1.ID,
		testdb.Ann.ID,
		events.NewAirtimeTransferred(&flows.AirtimeTransfer{
			ExternalID: "2237512891",
			Sender:     urns.NilURN,
			Recipient:  urns.URN("tel:+250700000002"),
			Currency:   "",
			Amount:     decimal.Zero,
		}, nil),
	)
	err = models.InsertAirtimeTransfers(ctx, rt.DB, []*models.AirtimeTransfer{transfer})
	assert.Nil(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) from airtime_airtimetransfer WHERE org_id = $1 AND status = $2`, testdb.Org1.ID, models.AirtimeTransferStatusFailed).Returns(1)
}
