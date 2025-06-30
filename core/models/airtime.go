package models

import (
	"context"
	"database/sql/driver"
	"time"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/null/v3"
	"github.com/shopspring/decimal"
)

// AirtimeTransferID is the type for airtime transfer IDs
type AirtimeTransferID int

// NilAirtimeTransferID is the nil value for airtime transfer IDs
var NilAirtimeTransferID = AirtimeTransferID(0)

// AirtimeTransferStatus is the type for the status of a transfer
type AirtimeTransferStatus string

const (
	// AirtimeTransferStatusSuccess is our status for successful transfers
	AirtimeTransferStatusSuccess AirtimeTransferStatus = "S"

	// AirtimeTransferStatusFailed is our status for failed transfers
	AirtimeTransferStatusFailed AirtimeTransferStatus = "F"
)

// AirtimeTransfer is our type for an airtime transfer
type AirtimeTransfer struct {
	t struct {
		ID            AirtimeTransferID     `db:"id"`
		UUID          flows.TransferUUID    `db:"uuid"`
		OrgID         OrgID                 `db:"org_id"`
		Status        AirtimeTransferStatus `db:"status"`
		ExternalID    null.String           `db:"external_id"`
		ContactID     ContactID             `db:"contact_id"`
		Sender        null.String           `db:"sender"`
		Recipient     urns.URN              `db:"recipient"`
		Currency      null.String           `db:"currency"`
		DesiredAmount decimal.Decimal       `db:"desired_amount"`
		ActualAmount  decimal.Decimal       `db:"actual_amount"`
		CreatedOn     time.Time             `db:"created_on"`
	}

	Logs []*HTTPLog
}

// NewAirtimeTransfer creates a new airtime transfer returning the result
func NewAirtimeTransfer(orgID OrgID, contactID ContactID, event *events.AirtimeTransferred) *AirtimeTransfer {
	status := AirtimeTransferStatusSuccess
	if event.Amount == decimal.Zero {
		status = AirtimeTransferStatusFailed
	}

	t := &AirtimeTransfer{}
	t.t.UUID = event.TransferUUID
	t.t.OrgID = orgID
	t.t.ContactID = contactID
	t.t.Status = status
	t.t.ExternalID = null.String(event.ExternalID)
	t.t.Sender = null.String(string(event.Sender))
	t.t.Recipient = event.Recipient
	t.t.Currency = null.String(string(event.Currency))
	t.t.DesiredAmount = event.Amount
	t.t.ActualAmount = event.Amount
	t.t.CreatedOn = event.CreatedOn()
	return t
}

func (t *AirtimeTransfer) ID() AirtimeTransferID {
	return t.t.ID
}

func (t *AirtimeTransfer) AddLog(l *HTTPLog) {
	t.Logs = append(t.Logs, l)
}

const sqlInsertAirtimeTransfers = `
INSERT INTO airtime_airtimetransfer(uuid,  org_id,  status,  external_id,  contact_id,  sender,  recipient,  currency,  desired_amount,  actual_amount,  created_on)
					        VALUES(:uuid, :org_id, :status, :external_id, :contact_id, :sender, :recipient, :currency, :desired_amount, :actual_amount, :created_on)
RETURNING id
`

// InsertAirtimeTransfers inserts the passed in airtime transfers returning any errors encountered
func InsertAirtimeTransfers(ctx context.Context, db DBorTx, transfers []*AirtimeTransfer) error {
	if len(transfers) == 0 {
		return nil
	}

	ts := make([]any, len(transfers))
	for i := range transfers {
		ts[i] = &transfers[i].t
	}

	return BulkQuery(ctx, "inserted airtime transfers", db, sqlInsertAirtimeTransfers, ts)
}

func (i *AirtimeTransferID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i AirtimeTransferID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *AirtimeTransferID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i AirtimeTransferID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
