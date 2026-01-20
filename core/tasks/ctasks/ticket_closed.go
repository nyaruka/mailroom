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
	"github.com/nyaruka/mailroom/runtime"
)

const TypeTicketClosed = "ticket_closed"

func init() {
	RegisterType(TypeTicketClosed, func() Task { return &TicketClosed{} })
}

type TicketClosed struct {
	Event *events.TicketClosed `json:"event"`
}

func NewTicketClosed(evt *events.TicketClosed) *TicketClosed {
	return &TicketClosed{
		Event: evt,
	}
}

func (t *TicketClosed) Type() string {
	return TypeTicketClosed
}

func (t *TicketClosed) UseReadOnly() bool {
	return false
}

func (t *TicketClosed) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	// load our ticket
	tickets, err := models.LoadTickets(ctx, rt.DB, oa.OrgID(), []flows.TicketUUID{t.Event.TicketUUID})
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
		slog.Debug("ignoring ticket closed event, no trigger found", "ticket", t.Event.TicketUUID)
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

	ticket := tickets[0].EngineTicket(oa)

	scene := runner.NewScene(mc, contact)

	// build our flow trigger
	flowTrigger := triggers.NewBuilder(flow.Reference()).TicketClosed(t.Event, ticket).Build()

	// if this is a voice flow, we request a call and wait for callback
	if flow.FlowType() == models.FlowTypeVoice {
		if _, err := ivr.RequestCall(ctx, rt, oa, mc, flowTrigger); err != nil {
			return fmt.Errorf("error starting voice flow for contact: %w", err)
		}
		return nil
	}

	if err := scene.StartSession(ctx, rt, oa, flowTrigger, flow.FlowType().Interrupts()); err != nil {
		return fmt.Errorf("error starting session for contact %s: %w", scene.ContactUUID(), err)
	}
	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene for contact %s: %w", scene.ContactUUID(), err)
	}

	return nil
}
