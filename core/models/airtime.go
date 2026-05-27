package models

import (
	"context"
	"database/sql"
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
	// AirtimeTransferStatusPending is our status for transfers awaiting provider confirmation
	AirtimeTransferStatusPending AirtimeTransferStatus = "P"

	// AirtimeTransferStatusSuccess is our status for successful transfers
	AirtimeTransferStatusSuccess AirtimeTransferStatus = "S"

	// AirtimeTransferStatusFailed is our status for failed transfers
	AirtimeTransferStatusFailed AirtimeTransferStatus = "F"

	// AirtimeTransferStatusReversed is our status for transfers reversed after success
	AirtimeTransferStatusReversed AirtimeTransferStatus = "R"
)

// AirtimeTransfer is our type for an airtime transfer
type AirtimeTransfer struct {
	t struct {
		ID            AirtimeTransferID     `db:"id"`
		UUID          flows.EventUUID       `db:"uuid"`
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

// NewAirtimeTransfer creates a new airtime transfer in the pending state, with the provider's transaction id
// (if any) already populated from the event so callbacks can correlate.
func NewAirtimeTransfer(orgID OrgID, contactID ContactID, event *events.AirtimeCreated) *AirtimeTransfer {
	t := &AirtimeTransfer{}
	t.t.UUID = event.UUID()
	t.t.OrgID = orgID
	t.t.ContactID = contactID
	t.t.Status = AirtimeTransferStatusPending
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

func (t *AirtimeTransfer) UUID() flows.EventUUID {
	return t.t.UUID
}

func (t *AirtimeTransfer) ExternalID() string {
	return string(t.t.ExternalID)
}

func (t *AirtimeTransfer) Status() AirtimeTransferStatus {
	return t.t.Status
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

// allowedAirtimeTransitions is the set of (current, new) status pairs the host accepts. Anything else is
// a no-op (e.g. a duplicate or out-of-order callback). Once a row reaches a terminal state we still allow
// success → reversed since DT One can reverse a transfer after it completes; everything else is locked.
var allowedAirtimeTransitions = map[AirtimeTransferStatus]map[AirtimeTransferStatus]bool{
	AirtimeTransferStatusPending: {
		AirtimeTransferStatusSuccess:  true,
		AirtimeTransferStatusFailed:   true,
		AirtimeTransferStatusReversed: true,
	},
	AirtimeTransferStatusSuccess: {
		AirtimeTransferStatusReversed: true,
	},
}

const sqlUpdateAirtimeTransferStatus = `
UPDATE airtime_airtimetransfer SET status = $2 WHERE uuid = $1 AND status = $3
`

// UpdateAirtimeTransferStatus transitions an airtime transfer to the given status, returning true if a
// row was actually updated. The transition is only applied if it's in the allowed set — late or duplicate
// callbacks that would walk the status backwards are ignored and return false with no error.
func UpdateAirtimeTransferStatus(ctx context.Context, db DBorTx, uuid flows.EventUUID, current, next AirtimeTransferStatus) (bool, error) {
	if !allowedAirtimeTransitions[current][next] {
		return false, nil
	}
	res, err := db.ExecContext(ctx, sqlUpdateAirtimeTransferStatus, uuid, next, current)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

const sqlSelectAirtimeTransferByExternalID = `
SELECT id, uuid, org_id, status, external_id, contact_id, sender, recipient, currency, desired_amount, actual_amount, created_on
  FROM airtime_airtimetransfer
 WHERE external_id = $1
 LIMIT 1
`

// GetAirtimeTransferByExternalID fetches the airtime transfer for the given provider transaction id.
// Returns nil with no error if no matching row exists.
func GetAirtimeTransferByExternalID(ctx context.Context, db DBorTx, externalID string) (*AirtimeTransfer, error) {
	t := &AirtimeTransfer{}
	err := db.GetContext(ctx, &t.t, sqlSelectAirtimeTransferByExternalID, externalID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (i *AirtimeTransferID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i AirtimeTransferID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *AirtimeTransferID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i AirtimeTransferID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
