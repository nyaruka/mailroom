package testdb

import (
	"context"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/stretchr/testify/require"
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

func (k *Ticket) Load(t *testing.T, rt *runtime.Runtime, org *Org) *models.Ticket {
	tickets, err := models.LoadTickets(context.Background(), rt.DB, org.ID, []flows.TicketUUID{k.UUID})
	require.NoError(t, err)
	require.Len(t, tickets, 1)
	return tickets[0]
}

// InsertOpenTicket inserts an open ticket
func InsertOpenTicket(t *testing.T, rt *runtime.Runtime, uuid flows.TicketUUID, org *Org, contact *Contact, topic *Topic, openedOn time.Time, assignee *User) *Ticket {
	return insertTicket(t, rt, uuid, org, contact, models.TicketStatusOpen, topic, openedOn, assignee)
}

// InsertClosedTicket inserts a closed ticket
func InsertClosedTicket(t *testing.T, rt *runtime.Runtime, uuid flows.TicketUUID, org *Org, contact *Contact, topic *Topic, assignee *User) *Ticket {
	return insertTicket(t, rt, uuid, org, contact, models.TicketStatusClosed, topic, dates.Now(), assignee)
}

func insertTicket(t *testing.T, rt *runtime.Runtime, uuid flows.TicketUUID, org *Org, contact *Contact, status models.TicketStatus, topic *Topic, openedOn time.Time, assignee *User) *Ticket {
	lastActivityOn := openedOn
	var closedOn *time.Time
	if status == models.TicketStatusClosed {
		t := dates.Now()
		lastActivityOn = t
		closedOn = &t
	}

	var id models.TicketID
	err := rt.DB.Get(&id,
		`INSERT INTO tickets_ticket(uuid, org_id, contact_id, status, topic_id, opened_on, modified_on, closed_on, last_activity_on, assignee_id)
		VALUES($1, $2, $3, $4, $5, $6, NOW(), $7, $8, $9) RETURNING id`, uuid, org.ID, contact.ID, status, topic.ID, openedOn, closedOn, lastActivityOn, assignee.SafeID(),
	)
	require.NoError(t, err)
	return &Ticket{id, uuid}
}
