package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
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

// airtimeTransferStatusNames maps each status to the lowercase name written to the history table and read
// back by clients (see https://github.com/nyaruka/temba-components). Clients inject this as the _status of
// the airtime_created event, mirroring how courier records msg_created status changes.
var airtimeTransferStatusNames = map[AirtimeTransferStatus]string{
	AirtimeTransferStatusCreated:   "created",
	AirtimeTransferStatusConfirmed: "confirmed",
	AirtimeTransferStatusSubmitted: "submitted",
	AirtimeTransferStatusCompleted: "completed",
	AirtimeTransferStatusReversed:  "reversed",
	AirtimeTransferStatusRejected:  "rejected",
	AirtimeTransferStatusCancelled: "cancelled",
	AirtimeTransferStatusDeclined:  "declined",
}

// AirtimeTransfer is our type for an airtime transfer
type AirtimeTransfer struct {
	t struct {
		ID            AirtimeTransferID     `db:"id"`
		UUID          events.EventUUID      `db:"uuid"`
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

func (t *AirtimeTransfer) UUID() events.EventUUID {
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

const sqlUpdateAirtimeTransferStatus = `
   UPDATE airtime_airtimetransfer t SET status = $2
     FROM contacts_contact c
    WHERE t.uuid = $1 AND t.external_id = $3 AND c.id = t.contact_id
RETURNING t.org_id AS org_id, c.uuid AS contact_uuid`

// UpdateAirtimeTransferStatus writes the given status to the airtime transfer keyed by (uuid, external_id).
// It returns an event tag recording the change for the contact's history (to be queued to the history table),
// or nil if no row was updated. external_id must match the row as defense in depth for the public callback
// path — a forged callback with a leaked UUID still can't mutate a row whose tx id it doesn't know. There's
// no status-transition guard: provider callbacks can arrive out of order or with gaps in the lifecycle (e.g.
// a Reversed callback for a transfer we never saw Completed for), and dropping those would strand the row in
// its prior state forever. We'd rather a late callback overwrite with stale state than silently lose a real
// terminal status update.
func UpdateAirtimeTransferStatus(ctx context.Context, db DBorTx, uuid events.EventUUID, externalID string, next AirtimeTransferStatus) (*EventTag, error) {
	row := &struct {
		OrgID       OrgID            `db:"org_id"`
		ContactUUID core.ContactUUID `db:"contact_uuid"`
	}{}
	if err := db.GetContext(ctx, row, sqlUpdateAirtimeTransferStatus, uuid, next, externalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return NewAirtimeStatusTag(row.OrgID, row.ContactUUID, uuid, next), nil
}

// NewAirtimeStatusTag creates the history-table event tag that records a transfer's status change. It's keyed
// by the same UUID as the transfer's airtime_created event (and shares a sort key across changes, so the
// latest overwrites) allowing clients to inject the current _status when rendering that event.
func NewAirtimeStatusTag(orgID OrgID, contactUUID core.ContactUUID, transferUUID events.EventUUID, status AirtimeTransferStatus) *EventTag {
	return &EventTag{
		OrgID:       orgID,
		ContactUUID: contactUUID,
		EventUUID:   transferUUID,
		Tag:         eventTagStatus,
		Data: map[string]any{
			"created_on": dates.Now(),
			"status":     airtimeTransferStatusNames[status],
		},
	}
}

func (i *AirtimeTransferID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i AirtimeTransferID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *AirtimeTransferID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i AirtimeTransferID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
