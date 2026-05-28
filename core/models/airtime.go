package models

import (
	"context"
	"database/sql/driver"
	"time"

	"github.com/lib/pq"
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

// AirtimeTransferStatus is the type for the status of a transfer. The values mirror the lifecycle a
// transfer goes through with a two-step provider: from the host's create call all the way through
// provider-reported delivery (or one of the various terminal failure states). See
// https://dvs-api-doc.dtone.com/#section/Overview/Transactions for the DT One taxonomy this is based on.
type AirtimeTransferStatus string

const (
	// AirtimeTransferStatusCreated — the host has created the transaction with the provider but not
	// yet confirmed it. New transfers start here.
	AirtimeTransferStatusCreated AirtimeTransferStatus = "P"

	// AirtimeTransferStatusConfirmed — the host has confirmed the transaction; the provider is now
	// responsible for sending it.
	AirtimeTransferStatusConfirmed AirtimeTransferStatus = "C"

	// AirtimeTransferStatusSubmitted — the provider has submitted the transaction to the operator's
	// network and is waiting on confirmation of delivery.
	AirtimeTransferStatusSubmitted AirtimeTransferStatus = "B"

	// AirtimeTransferStatusCompleted — the operator confirmed delivery.
	AirtimeTransferStatusCompleted AirtimeTransferStatus = "S"

	// AirtimeTransferStatusReversed — a previously completed transfer has been reversed by the provider.
	AirtimeTransferStatusReversed AirtimeTransferStatus = "R"

	// AirtimeTransferStatusRejected — the operator (or the provider) rejected the transaction.
	AirtimeTransferStatusRejected AirtimeTransferStatus = "J"

	// AirtimeTransferStatusCancelled — the transaction was cancelled before delivery (e.g. the
	// provider's hold expired without a Confirm).
	AirtimeTransferStatusCancelled AirtimeTransferStatus = "A"

	// AirtimeTransferStatusDeclined — the operator declined the transaction.
	AirtimeTransferStatusDeclined AirtimeTransferStatus = "D"
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
	t.t.Status = AirtimeTransferStatusCreated
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

// allowedAirtimePredecessors lists the statuses a row may be in for each destination status to be a
// valid transition. Anything not listed is a no-op — late or duplicate callbacks that would walk the
// status backwards in the lifecycle (e.g. S → C, R → S) are ignored. The lifecycle is roughly:
//
//	Created → Confirmed → Submitted → Completed → Reversed
//	                               ↘
//	                                Rejected / Cancelled / Declined
//
// Any non-terminal status can transition to any later non-terminal status, or to any terminal failure
// state. Once a row reaches Reversed / Rejected / Cancelled / Declined it's locked.
var allowedAirtimePredecessors = map[AirtimeTransferStatus][]AirtimeTransferStatus{
	AirtimeTransferStatusConfirmed: {AirtimeTransferStatusCreated},
	AirtimeTransferStatusSubmitted: {AirtimeTransferStatusCreated, AirtimeTransferStatusConfirmed},
	AirtimeTransferStatusCompleted: {AirtimeTransferStatusCreated, AirtimeTransferStatusConfirmed, AirtimeTransferStatusSubmitted},
	AirtimeTransferStatusRejected:  {AirtimeTransferStatusCreated, AirtimeTransferStatusConfirmed, AirtimeTransferStatusSubmitted},
	AirtimeTransferStatusCancelled: {AirtimeTransferStatusCreated, AirtimeTransferStatusConfirmed, AirtimeTransferStatusSubmitted},
	AirtimeTransferStatusDeclined:  {AirtimeTransferStatusCreated, AirtimeTransferStatusConfirmed, AirtimeTransferStatusSubmitted},
	AirtimeTransferStatusReversed:  {AirtimeTransferStatusCompleted},
}

const sqlUpdateAirtimeTransferStatus = `
UPDATE airtime_airtimetransfer
   SET status = $2
 WHERE uuid = $1
   AND external_id = $3
   AND status = ANY($4::text[])
`

// UpdateAirtimeTransferStatus transitions an airtime transfer to the given status, returning true if a
// row was actually updated. The current status is matched in the same statement against the destination's
// allowed predecessors — so concurrent callbacks race safely on a single compare-and-swap rather than via
// a separate SELECT (which would leave a TOCTOU window where a legitimate transition could be silently
// dropped). external_id must also match the row, which serves as defense in depth for the public callback
// path: a forged callback now needs to know both the row's UUID (122 bits) and its provider tx id, not
// just one. Returns false with no error when the row is already in a terminal state that doesn't admit
// the requested transition, or when external_id doesn't match the row.
func UpdateAirtimeTransferStatus(ctx context.Context, db DBorTx, uuid flows.EventUUID, externalID string, next AirtimeTransferStatus) (bool, error) {
	preds, ok := allowedAirtimePredecessors[next]
	if !ok {
		return false, nil
	}
	predStrs := make([]string, len(preds))
	for i, p := range preds {
		predStrs[i] = string(p)
	}
	res, err := db.ExecContext(ctx, sqlUpdateAirtimeTransferStatus, uuid, next, externalID, pq.Array(predStrs))
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (i *AirtimeTransferID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i AirtimeTransferID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *AirtimeTransferID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i AirtimeTransferID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
