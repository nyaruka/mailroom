package models

import (
	"context"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/null/v3"
)

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
	UUID        flows.EventUUID `json:"uuid"                    db:"uuid"`
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

func NewTicketOpenedEvent(uuid flows.EventUUID, t *Ticket, userID UserID, assigneeID UserID, note string) *TicketEvent {
	return newTicketEvent(uuid, t.OrgID, t.ContactID, t.ID, userID, TicketEventTypeOpened, note, NilTopicID, assigneeID)
}

func NewTicketAssignedEvent(uuid flows.EventUUID, t *Ticket, userID UserID, assigneeID UserID) *TicketEvent {
	return newTicketEvent(uuid, t.OrgID, t.ContactID, t.ID, userID, TicketEventTypeAssigned, "", NilTopicID, assigneeID)
}

func NewTicketNoteAddedEvent(uuid flows.EventUUID, t *Ticket, userID UserID, note string) *TicketEvent {
	return newTicketEvent(uuid, t.OrgID, t.ContactID, t.ID, userID, TicketEventTypeNoteAdded, note, NilTopicID, NilUserID)
}

func NewTicketTopicChangedEvent(uuid flows.EventUUID, t *Ticket, userID UserID, topicID TopicID) *TicketEvent {
	return newTicketEvent(uuid, t.OrgID, t.ContactID, t.ID, userID, TicketEventTypeTopicChanged, "", topicID, NilUserID)
}

func NewTicketClosedEvent(uuid flows.EventUUID, t *Ticket, userID UserID) *TicketEvent {
	return newTicketEvent(uuid, t.OrgID, t.ContactID, t.ID, userID, TicketEventTypeClosed, "", NilTopicID, NilUserID)
}

func NewTicketReopenedEvent(uuid flows.EventUUID, t *Ticket, userID UserID) *TicketEvent {
	return newTicketEvent(uuid, t.OrgID, t.ContactID, t.ID, userID, TicketEventTypeReopened, "", NilTopicID, NilUserID)
}

func newTicketEvent(uuid flows.EventUUID, orgID OrgID, contactID ContactID, ticketID TicketID, userID UserID, eventType TicketEventType, note string, topicID TopicID, assigneeID UserID) *TicketEvent {
	return &TicketEvent{
		UUID:        uuid,
		OrgID:       orgID,
		ContactID:   contactID,
		TicketID:    ticketID,
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
RETURNING id`

func InsertLegacyTicketEvents(ctx context.Context, db DBorTx, evts []*TicketEvent) error {
	return BulkQuery(ctx, "inserting ticket events", db, sqlInsertTicketEvents, evts)
}
