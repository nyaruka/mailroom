package ctasks

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeTicketClosed = "ticket_closed"

func init() {
	handler.RegisterContactTask(TypeTicketClosed, func() handler.Task { return &TicketClosedTask{} })
}

type TicketClosedTask struct {
	TicketID models.TicketID `json:"ticket_id"`
}

func NewTicketClosed(ticketID models.TicketID) *TicketClosedTask {
	return &TicketClosedTask{TicketID: ticketID}
}

func (t *TicketClosedTask) Type() string {
	return TypeTicketClosed
}

func (t *TicketClosedTask) UseReadOnly() bool {
	return false
}

func (t *TicketClosedTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	// load our ticket
	tickets, err := models.LoadTickets(ctx, rt.DB, []models.TicketID{t.TicketID})
	if err != nil {
		return fmt.Errorf("error loading ticket: %w", err)
	}
	// ticket has been deleted ignore this event
	if len(tickets) == 0 {
		return nil
	}

	// build our flow contact
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	// do we have associated trigger?
	trigger := models.FindMatchingTicketClosedTrigger(oa, contact)

	// no trigger, noop, move on
	if trigger == nil {
		slog.Info("ignoring ticket closed event, no trigger found", "ticket_id", t.TicketID)
		return nil
	}

	// load our flow
	flow, err := oa.FlowByID(trigger.FlowID())
	if err == models.ErrNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("error loading flow for trigger: %w", err)
	}

	ticket := tickets[0].FlowTicket(oa)
	evt := events.NewTicketClosed(ticket)

	scene := runner.NewScene(mc, contact, models.NilUserID)
	scene.AddEvents([]flows.Event{evt})

	// build our flow trigger
	flowTrigger := triggers.NewBuilder(flow.Reference()).Ticket(ticket, evt).Build()

	// if this is a voice flow, we request a call and wait for callback
	if flow.FlowType() == models.FlowTypeVoice {
		if _, err := ivr.RequestCall(ctx, rt, oa, mc, flowTrigger); err != nil {
			return fmt.Errorf("error starting voice flow for contact: %w", err)
		}
		return nil
	}

	err = runner.StartSessions(ctx, rt, oa, []*runner.Scene{scene}, nil, []flows.Trigger{flowTrigger}, flow.FlowType().Interrupts(), models.NilStartID)
	if err != nil {
		return fmt.Errorf("error starting flow for contact: %w", err)
	}
	return nil
}
