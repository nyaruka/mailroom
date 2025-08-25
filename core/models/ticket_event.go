package models

import (
	"context"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/null/v3"
)

type TicketEventUUID uuids.UUID
type TicketEventID int
type TicketEventType string

const (
	TicketEventTypeOpened       TicketEventType = "O"
	TicketEventTypeAssigned     TicketEventType = "A"
	TicketEventTypeNoteAdded    TicketEventType = "N"
	TicketEventTypeTopicChanged TicketEventType = "T"
	TicketEventTypeClosed       TicketEventType = "C"
	TicketEventTypeReopened     TicketEventType = "R"
)

type TicketEvent struct {
	UUID        TicketEventUUID `json:"uuid"                    db:"uuid"`
	ID          TicketEventID   `json:"id"                      db:"id"`
	OrgID       OrgID           `json:"org_id"                  db:"org_id"`
	ContactID   ContactID       `json:"contact_id"              db:"contact_id"`
	TicketID    TicketID        `json:"ticket_id"               db:"ticket_id"`
	Type        TicketEventType `json:"event_type"              db:"event_type"`
	Note        null.String     `json:"note,omitempty"          db:"note"`
	TopicID     TopicID         `json:"topic_id,omitempty"      db:"topic_id"`
	AssigneeID  UserID          `json:"assignee_id,omitempty"   db:"assignee_id"`
	CreatedByID UserID          `json:"created_by_id,omitempty" db:"created_by_id"`
	CreatedOn   time.Time       `json:"created_on"              db:"created_on"`
}

func NewTicketOpenedEvent(t *Ticket, userID UserID, assigneeID UserID, note string) *TicketEvent {
	return newTicketEvent(t, userID, TicketEventTypeOpened, note, NilTopicID, assigneeID)
}

func NewTicketAssignedEvent(t *Ticket, userID UserID, assigneeID UserID) *TicketEvent {
	return newTicketEvent(t, userID, TicketEventTypeAssigned, "", NilTopicID, assigneeID)
}

func NewTicketNoteAddedEvent(t *Ticket, userID UserID, note string) *TicketEvent {
	return newTicketEvent(t, userID, TicketEventTypeNoteAdded, note, NilTopicID, NilUserID)
}

func NewTicketTopicChangedEvent(t *Ticket, userID UserID, topicID TopicID) *TicketEvent {
	return newTicketEvent(t, userID, TicketEventTypeTopicChanged, "", topicID, NilUserID)
}

func NewTicketClosedEvent(t *Ticket, userID UserID) *TicketEvent {
	return newTicketEvent(t, userID, TicketEventTypeClosed, "", NilTopicID, NilUserID)
}

func NewTicketReopenedEvent(t *Ticket, userID UserID) *TicketEvent {
	return newTicketEvent(t, userID, TicketEventTypeReopened, "", NilTopicID, NilUserID)
}

func newTicketEvent(t *Ticket, userID UserID, eventType TicketEventType, note string, topicID TopicID, assigneeID UserID) *TicketEvent {
	return &TicketEvent{
		UUID:        TicketEventUUID(uuids.NewV7()),
		OrgID:       t.OrgID(),
		ContactID:   t.ContactID(),
		TicketID:    t.ID(),
		Type:        eventType,
		Note:        null.String(note),
		TopicID:     topicID,
		AssigneeID:  assigneeID,
		CreatedOn:   dates.Now(),
		CreatedByID: userID,
	}
}

const sqlInsertTicketEvents = `
INSERT INTO
	tickets_ticketevent(uuid,  org_id,  contact_id,  ticket_id,  event_type,  note,  topic_id,  assignee_id,  created_on,  created_by_id)
	            VALUES(:uuid, :org_id, :contact_id, :ticket_id, :event_type, :note, :topic_id, :assignee_id, :created_on, :created_by_id)
RETURNING
	id
`

func InsertTicketEvents(ctx context.Context, db DBorTx, evts []*TicketEvent) error {
	return BulkQuery(ctx, "inserting ticket events", db, sqlInsertTicketEvents, evts)
}
