package models

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/null/v3"
)

type ChannelEventID int64
type ChannelEventType string
type ChannelEventStatus string
type ChannelEventUUID uuids.UUID

const (
	NilChannelEventID ChannelEventID = 0

	// channel event types
	EventTypeNewConversation ChannelEventType = "new_conversation"
	EventTypeWelcomeMessage  ChannelEventType = "welcome_message"
	EventTypeReferral        ChannelEventType = "referral"
	EventTypeMissedCall      ChannelEventType = "mo_miss"
	EventTypeIncomingCall    ChannelEventType = "mo_call"
	EventTypeStopContact     ChannelEventType = "stop_contact"
	EventTypeOptIn           ChannelEventType = "optin"
	EventTypeOptOut          ChannelEventType = "optout"
	EventTypeDeleteContact   ChannelEventType = "delete_contact"

	// channel event statuses
	EventStatusPending ChannelEventStatus = "P" // event created but not yet handled
	EventStatusHandled ChannelEventStatus = "H" // event handled
)

// ContactSeenEvents are those which count as the contact having been seen
var ContactSeenEvents = map[ChannelEventType]bool{
	EventTypeNewConversation: true,
	EventTypeReferral:        true,
	EventTypeMissedCall:      true,
	EventTypeIncomingCall:    true,
	EventTypeStopContact:     true,
	EventTypeOptIn:           true,
	EventTypeOptOut:          true,
}

// ChannelEvent represents an event that occurred associated with a channel, such as a referral, missed call, etc..
type ChannelEvent struct {
	ID         ChannelEventID     `db:"id"`
	UUID       ChannelEventUUID   `db:"uuid"`
	EventType  ChannelEventType   `db:"event_type"`
	Status     ChannelEventStatus `db:"status"`
	OrgID      OrgID              `db:"org_id"`
	ChannelID  ChannelID          `db:"channel_id"`
	ContactID  ContactID          `db:"contact_id"`
	URNID      URNID              `db:"contact_urn_id"`
	OptInID    OptInID            `db:"optin_id"`
	Extra      null.Map[any]      `db:"extra"`
	OccurredOn time.Time          `db:"occurred_on"`
	CreatedOn  time.Time          `db:"created_on"`
}

const sqlInsertChannelEvent = `
INSERT INTO channels_channelevent(uuid, org_id,  event_type,  status,  channel_id,  contact_id,  contact_urn_id,  optin_id,  extra, created_on, occurred_on)
	                       VALUES(:uuid, :org_id, :event_type, :status, :channel_id, :contact_id, :contact_urn_id, :optin_id, :extra, NOW(),     :occurred_on)
  RETURNING id, created_on`

// Insert inserts this channel event to our DB. The ID of the channel event will be
// set if no error is returned
func (e *ChannelEvent) Insert(ctx context.Context, db DBorTx) error {
	return BulkQuery(ctx, "insert channel event", db, sqlInsertChannelEvent, []any{e})
}

// NewChannelEvent creates a new channel event for the passed in parameters, returning it
func NewChannelEvent(orgID OrgID, eventType ChannelEventType, channelID ChannelID, contactID ContactID, urnID URNID, status ChannelEventStatus, extra map[string]any, occurredOn time.Time) *ChannelEvent {
	e := &ChannelEvent{
		UUID:       ChannelEventUUID(uuids.NewV7()),
		OrgID:      orgID,
		EventType:  eventType,
		ChannelID:  channelID,
		ContactID:  contactID,
		URNID:      urnID,
		Status:     status,
		OccurredOn: occurredOn,
	}

	if extra == nil {
		e.Extra = null.Map[any]{}
	} else {
		e.Extra = null.Map[any](extra)
	}

	return e
}

// MarkChannelEventHandled updates a channel event after handling
func MarkChannelEventHandled(ctx context.Context, tx DBorTx, uuid ChannelEventUUID) error {
	_, err := tx.ExecContext(ctx, `UPDATE channels_channelevent SET status = 'H' WHERE uuid = $1`, uuid)
	if err != nil {
		return fmt.Errorf("error marking event as handled: %w", err)
	}
	return nil
}
