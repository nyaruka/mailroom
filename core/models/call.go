package models

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/null/v3"
)

// CallID is the type for call IDs
type CallID int

// NilCallID is the nil value for call IDs
const NilCallID = CallID(0)

// CallStatus is the type for the status of a call
type CallStatus string

// CallError is the type for the reason of an errored call
type CallError null.String

// call status constants
const (
	CallStatusPending    = CallStatus("P") // used for initial creation in database
	CallStatusQueued     = CallStatus("Q") // call can't be wired yet and is queued locally
	CallStatusWired      = CallStatus("W") // call has been requested on the IVR provider
	CallStatusInProgress = CallStatus("I") // call was answered and is in progress
	CallStatusCompleted  = CallStatus("D") // call was completed successfully
	CallStatusErrored    = CallStatus("E") // temporary failure (will be retried)
	CallStatusFailed     = CallStatus("F") // permanent failure

	CallErrorProvider = CallError("P")
	CallErrorBusy     = CallError("B")
	CallErrorNoAnswer = CallError("N")
	CallErrorMachine  = CallError("M")

	CallMaxRetries = 3

	// CallRetryWait is our default wait to retry call requests
	CallRetryWait = time.Minute * 60

	// CallThrottleWait is our wait between throttle retries
	CallThrottleWait = time.Minute * 2
)

// Call models an IVR call
type Call struct {
	c struct {
		ID           CallID      `db:"id"`
		OrgID        OrgID       `db:"org_id"`
		ChannelID    ChannelID   `db:"channel_id"`
		ContactID    ContactID   `db:"contact_id"`
		ContactURNID URNID       `db:"contact_urn_id"`
		ExternalID   string      `db:"external_id"`
		Status       CallStatus  `db:"status"`
		SessionUUID  null.String `db:"session_uuid"`
		Direction    Direction   `db:"direction"`
		StartedOn    *time.Time  `db:"started_on"`
		EndedOn      *time.Time  `db:"ended_on"`
		Duration     int         `db:"duration"`
		ErrorReason  null.String `db:"error_reason"`
		ErrorCount   int         `db:"error_count"`
		NextAttempt  *time.Time  `db:"next_attempt"`
		Trigger      null.JSON   `db:"trigger"`
		CreatedOn    time.Time   `db:"created_on"`
		ModifiedOn   time.Time   `db:"modified_on"`
	}
}

func (c *Call) ID() CallID                     { return c.c.ID }
func (c *Call) ChannelID() ChannelID           { return c.c.ChannelID }
func (c *Call) OrgID() OrgID                   { return c.c.OrgID }
func (c *Call) ContactID() ContactID           { return c.c.ContactID }
func (c *Call) ContactURNID() URNID            { return c.c.ContactURNID }
func (c *Call) Direction() Direction           { return c.c.Direction }
func (c *Call) Status() CallStatus             { return c.c.Status }
func (c *Call) SessionUUID() flows.SessionUUID { return flows.SessionUUID(c.c.SessionUUID) }
func (c *Call) ExternalID() string             { return c.c.ExternalID }
func (c *Call) ErrorReason() CallError         { return CallError(c.c.ErrorReason) }
func (c *Call) ErrorCount() int                { return c.c.ErrorCount }
func (c *Call) NextAttempt() *time.Time        { return c.c.NextAttempt }

func (c *Call) EngineTrigger(oa *OrgAssets) (flows.Trigger, error) {
	trigger, err := triggers.ReadTrigger(oa.SessionAssets(), c.c.Trigger, assets.IgnoreMissing)
	if err != nil {
		return nil, fmt.Errorf("error reading call trigger: %w", err)
	}

	return trigger, nil
}

// NewIncomingCall creates a new incoming IVR call
func NewIncomingCall(orgID OrgID, ch *Channel, contact *Contact, urnID URNID, externalID string) *Call {
	call := &Call{}
	c := &call.c
	c.OrgID = orgID
	c.ChannelID = ch.ID()
	c.ContactID = contact.ID()
	c.ContactURNID = urnID
	c.Direction = DirectionIn
	c.Status = CallStatusInProgress
	c.ExternalID = externalID
	return call
}

// NewOutgoingCall creates a new outgoing IVR call
func NewOutgoingCall(orgID OrgID, ch *Channel, contact *Contact, urnID URNID, trigger flows.Trigger) *Call {
	call := &Call{}
	c := &call.c
	c.OrgID = orgID
	c.ChannelID = ch.ID()
	c.ContactID = contact.ID()
	c.ContactURNID = urnID
	c.Direction = DirectionOut
	c.Status = CallStatusPending
	c.Trigger = null.JSON(jsonx.MustMarshal(trigger))
	return call
}

const sqlInsertCall = `
INSERT INTO ivr_call( org_id,  channel_id,  contact_id,  contact_urn_id, created_on, modified_on,  external_id,  status,  direction,  trigger, duration, error_count)
              VALUES(:org_id, :channel_id, :contact_id, :contact_urn_id, NOW(),      NOW(),       :external_id, :status, :direction, :trigger, 0,        0)
  RETURNING id, created_on, modified_on;`

// InsertCalls creates a new IVR call for the passed in org, channel and contact, inserting it
func InsertCalls(ctx context.Context, db DBorTx, calls []*Call) error {
	is := make([]any, len(calls))
	for i, c := range calls {
		is[i] = &c.c
	}

	return BulkQueryBatches(ctx, "inserted IVR calls", db, sqlInsertCall, 1000, is)
}

const sqlSelectCallByID = `
SELECT
    cc.id,
	cc.org_id,
    cc.created_on,
    cc.modified_on,
    cc.external_id,
    cc.status,
    cc.direction,
    cc.started_on,
    cc.ended_on,
    cc.duration,
    cc.error_reason,
    cc.error_count,
    cc.next_attempt,
    cc.channel_id,
    cc.contact_id,
    cc.contact_urn_id,
    cc.session_uuid,
	cc.trigger
           FROM ivr_call as cc
          WHERE cc.org_id = $1 AND cc.id = $2`

// GetCallByID loads a call by id
func GetCallByID(ctx context.Context, db DBorTx, orgID OrgID, id CallID) (*Call, error) {
	c := &Call{}
	err := db.GetContext(ctx, &c.c, sqlSelectCallByID, orgID, id)
	if err != nil {
		return nil, fmt.Errorf("unable to load call with id: %d: %w", id, err)
	}
	return c, nil
}

const sqlSelectCallByExternalID = `
SELECT
    cc.id,
	cc.org_id,
    cc.created_on,
    cc.modified_on,
    cc.external_id,
    cc.status,
    cc.direction,
    cc.started_on,
    cc.ended_on,
    cc.duration,
    cc.error_reason,
    cc.error_count,
    cc.next_attempt,
    cc.channel_id,
    cc.contact_id,
    cc.contact_urn_id,
    cc.session_uuid,
	cc.trigger
           FROM ivr_call as cc
          WHERE cc.channel_id = $1 AND cc.external_id = $2
       ORDER BY cc.id DESC
          LIMIT 1`

// GetCallByExternalID loads a call by its external ID
func GetCallByExternalID(ctx context.Context, db DBorTx, channelID ChannelID, externalID string) (*Call, error) {
	c := &Call{}
	err := db.GetContext(ctx, &c.c, sqlSelectCallByExternalID, channelID, externalID)
	if err != nil {
		return nil, fmt.Errorf("unable to load call with external id: %s: %w", externalID, err)
	}
	return c, nil
}

const sqlSelectRetryCalls = `
SELECT
    cc.id,
	cc.org_id,
    cc.created_on,
    cc.modified_on,
    cc.external_id,
    cc.status,
    cc.direction,
    cc.started_on,
    cc.ended_on,
    cc.duration,
    cc.error_reason,
    cc.error_count,
    cc.next_attempt,
    cc.channel_id,
    cc.contact_id,
    cc.contact_urn_id,
    cc.session_uuid,
	cc.trigger
           FROM ivr_call as cc
          WHERE cc.status IN ('Q', 'E') AND next_attempt < NOW()
       ORDER BY cc.next_attempt ASC
          LIMIT $1`

// LoadCallsToRetry returns up to limit calls that need to be retried
func LoadCallsToRetry(ctx context.Context, db *sqlx.DB, limit int) ([]*Call, error) {
	rows, err := db.QueryxContext(ctx, sqlSelectRetryCalls, limit)
	if err != nil {
		return nil, fmt.Errorf("error selecting calls to retry: %w", err)
	}
	defer rows.Close()

	calls := make([]*Call, 0, 10)
	for rows.Next() {
		c := &Call{}
		err = rows.StructScan(&c.c)
		if err != nil {
			return nil, fmt.Errorf("error scanning call: %w", err)
		}
		calls = append(calls, c)
	}

	return calls, nil
}

// UpdateExternalID updates the external id on the passed in channel session
func (c *Call) UpdateExternalID(ctx context.Context, db DBorTx, id string) error {
	c.c.ExternalID = id
	c.c.Status = CallStatusWired

	_, err := db.ExecContext(ctx, `UPDATE ivr_call SET external_id = $2, status = $3, modified_on = NOW() WHERE id = $1`, c.c.ID, c.c.ExternalID, c.c.Status)
	if err != nil {
		return fmt.Errorf("error updating external id to: %s for call: %d: %w", c.c.ExternalID, c.c.ID, err)
	}

	return nil
}

// SetInProgress sets the status of this call to IN_PROGRESS and records the session UUID and started time
func (c *Call) SetInProgress(ctx context.Context, db DBorTx, sessionUUID flows.SessionUUID, now time.Time) error {
	c.c.Status = CallStatusInProgress
	c.c.SessionUUID = null.String(sessionUUID)
	c.c.StartedOn = &now

	_, err := db.ExecContext(ctx, `UPDATE ivr_call SET status = $2, session_uuid = $3, started_on = $4, modified_on = NOW() WHERE id = $1`, c.c.ID, c.c.Status, c.c.SessionUUID, c.c.StartedOn)
	if err != nil {
		return fmt.Errorf("error marking call as started: %w", err)
	}

	return nil
}

// SetErrored sets the status of this call to ERRORED and schedules a retry if appropriate
func (c *Call) SetErrored(ctx context.Context, db DBorTx, now time.Time, retryWait *time.Duration, errorReason CallError) error {
	c.c.Status = CallStatusErrored
	c.c.ErrorReason = null.String(errorReason)
	c.c.EndedOn = &now

	if c.c.ErrorCount < CallMaxRetries && retryWait != nil {
		c.c.ErrorCount++
		next := now.Add(*retryWait)
		c.c.NextAttempt = &next
	} else {
		c.c.Status = CallStatusFailed
		c.c.NextAttempt = nil
	}

	_, err := db.ExecContext(ctx,
		`UPDATE ivr_call SET status = $2, ended_on = $3, error_reason = $4, error_count = $5, next_attempt = $6, modified_on = NOW() WHERE id = $1`,
		c.c.ID, c.c.Status, c.c.EndedOn, c.c.ErrorReason, c.c.ErrorCount, c.c.NextAttempt,
	)

	if err != nil {
		return fmt.Errorf("error marking call as errored: %w", err)
	}

	return nil
}

// SetFailed sets this call to be failed, updating status and ended time
func (c *Call) SetFailed(ctx context.Context, db DBorTx) error {
	now := dates.Now()

	c.c.Status = CallStatusFailed
	c.c.EndedOn = &now

	_, err := db.ExecContext(ctx, `UPDATE ivr_call SET status = $2, ended_on = $3, modified_on = NOW() WHERE id = $1`, c.c.ID, c.c.Status, c.c.EndedOn)
	if err != nil {
		return fmt.Errorf("error setting call #%d failed: %w", c.c.ID, err)
	}

	return nil
}

// SetThrottled updates the status for this call to be queued, to be retried in a minute
func (c *Call) SetThrottled(ctx context.Context, db DBorTx) error {
	next := dates.Now().Add(CallThrottleWait)

	c.c.Status = CallStatusQueued
	c.c.NextAttempt = &next

	_, err := db.ExecContext(ctx, `UPDATE ivr_call SET status = $2, next_attempt = $3, modified_on = NOW() WHERE id = $1`, c.c.ID, c.c.Status, c.c.NextAttempt)
	if err != nil {
		return fmt.Errorf("error setting call #%d throttled: %w", c.c.ID, err)
	}

	return nil
}

// UpdateStatus updates the status for this call
func (c *Call) UpdateStatus(ctx context.Context, db DBorTx, status CallStatus, duration int, now time.Time) error {
	c.c.Status = status
	var err error

	// only write a duration if it is greater than 0
	if duration > 0 {
		c.c.Duration = duration
		c.c.EndedOn = &now
		_, err = db.ExecContext(ctx, `UPDATE ivr_call SET status = $2, duration = $3, ended_on = $4, modified_on = NOW() WHERE id = $1`, c.c.ID, c.c.Status, c.c.Duration, c.c.EndedOn)
	} else {
		_, err = db.ExecContext(ctx, `UPDATE ivr_call SET status = $2, modified_on = NOW() WHERE id = $1`, c.c.ID, c.c.Status)
	}

	if err != nil {
		return fmt.Errorf("error updating status for call: %d: %w", c.c.ID, err)
	}

	return nil
}

func (c *Call) AttachLog(ctx context.Context, db DBorTx, clog *ChannelLog) error {
	_, err := db.ExecContext(ctx, `UPDATE ivr_call SET log_uuids = array_append(log_uuids, $2) WHERE id = $1`, c.c.ID, clog.UUID)
	if err != nil {
		return fmt.Errorf("error attaching log to call: %w", err)
	}
	return nil
}

// ActiveCallCount returns the number of ongoing calls for the passed in channel
func ActiveCallCount(ctx context.Context, db DBorTx, id ChannelID) (int, error) {
	count := 0
	err := db.GetContext(ctx, &count, `SELECT count(*) FROM ivr_call WHERE channel_id = $1 AND (status = 'W' OR status = 'I')`, id)
	if err != nil {
		return 0, fmt.Errorf("unable to select active call count: %w", err)
	}
	return count, nil
}

func (i *CallID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i CallID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *CallID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i CallID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
