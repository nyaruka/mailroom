package testdb

import (
	"context"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type Topic struct {
	ID   models.TopicID
	UUID assets.TopicUUID
}

type Ticket struct {
	ID   models.TicketID
	UUID flows.TicketUUID
}

type Team struct {
	ID   models.TeamID
	UUID models.TeamUUID
}

func (k *Ticket) Load(rt *runtime.Runtime) *models.Ticket {
	tickets, err := models.LoadTickets(context.Background(), rt.DB, []models.TicketID{k.ID})
	must(err, len(tickets) == 1)
	return tickets[0]
}

// InsertOpenTicket inserts an open ticket
func InsertOpenTicket(rt *runtime.Runtime, uuid flows.TicketUUID, org *Org, contact *Contact, topic *Topic, openedOn time.Time, assignee *User) *Ticket {
	return insertTicket(rt, uuid, org, contact, models.TicketStatusOpen, topic, openedOn, assignee)
}

// InsertClosedTicket inserts a closed ticket
func InsertClosedTicket(rt *runtime.Runtime, uuid flows.TicketUUID, org *Org, contact *Contact, topic *Topic, assignee *User) *Ticket {
	return insertTicket(rt, uuid, org, contact, models.TicketStatusClosed, topic, dates.Now(), assignee)
}

func insertTicket(rt *runtime.Runtime, uuid flows.TicketUUID, org *Org, contact *Contact, status models.TicketStatus, topic *Topic, openedOn time.Time, assignee *User) *Ticket {
	lastActivityOn := openedOn
	var closedOn *time.Time
	if status == models.TicketStatusClosed {
		t := dates.Now()
		lastActivityOn = t
		closedOn = &t
	}

	var id models.TicketID
	must(rt.DB.Get(&id,
		`INSERT INTO tickets_ticket(uuid, org_id, contact_id, status, topic_id, opened_on, modified_on, closed_on, last_activity_on, assignee_id)
		VALUES($1, $2, $3, $4, $5, $6, NOW(), $7, $8, $9) RETURNING id`, uuid, org.ID, contact.ID, status, topic.ID, openedOn, closedOn, lastActivityOn, assignee.SafeID(),
	))
	return &Ticket{id, uuid}
}
