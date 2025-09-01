package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

type TicketAndNote struct {
	Event  *events.TicketOpened
	Ticket *models.Ticket
}

// InsertTickets is our hook for inserting tickets
var InsertTickets runner.PreCommitHook = &insertTickets{}

type insertTickets struct{}

func (h *insertTickets) Order() int { return 10 }

func (h *insertTickets) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our tickets and notes
	tickets := make([]*models.Ticket, 0, len(scenes))
	events := make(map[*models.Ticket]*events.TicketOpened, len(scenes))

	for _, ts := range scenes {
		for _, t := range ts {
			open := t.(TicketAndNote)
			tickets = append(tickets, open.Ticket)
			events[open.Ticket] = open.Event
		}
	}

	// insert the tickets
	if err := models.InsertTickets(ctx, tx, oa, tickets); err != nil {
		return fmt.Errorf("error inserting tickets: %w", err)
	}

	// generate legacy opened events for each ticket
	legacyEvents := make([]*models.TicketEvent, len(tickets))
	eventsByTicket := make(map[*models.Ticket]*models.TicketEvent, len(tickets))
	for i, ticket := range tickets {
		event := events[ticket]
		evt := models.NewTicketOpenedEvent(event.UUID(), ticket, ticket.OpenedByID(), ticket.AssigneeID(), event.Note)
		legacyEvents[i] = evt
		eventsByTicket[ticket] = evt
	}

	// and insert those too
	if err := models.InsertLegacyTicketEvents(ctx, tx, legacyEvents); err != nil {
		return fmt.Errorf("error inserting ticket opened events: %w", err)
	}

	// and insert logs/notifications for those
	if err := models.NotificationsFromTicketEvents(ctx, tx, oa, eventsByTicket); err != nil {
		return fmt.Errorf("error inserting notifications: %w", err)
	}

	return nil
}
