package ctasks

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
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
	fc, err := mc.FlowContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	// do we have associated trigger?
	trigger := models.FindMatchingTicketClosedTrigger(oa, fc)

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

	// if this is an IVR flow, we need to trigger that start (which happens in a different queue)
	if flow.FlowType() == models.FlowTypeVoice {
		err = handler.TriggerIVRFlow(ctx, rt, oa.OrgID(), flow.ID(), []models.ContactID{mc.ID()}, nil)
		if err != nil {
			return fmt.Errorf("error while triggering ivr flow: %w", err)
		}
		return nil
	}

	// build our flow ticket
	ticket := tickets[0].FlowTicket(oa)

	// build our flow trigger
	flowTrigger := triggers.NewBuilder(oa.Env(), flow.Reference(), fc).
		Ticket(ticket, triggers.TicketEventTypeClosed).
		Build()

	_, err = runner.StartFlow(ctx, rt, oa, flow, []*models.Contact{mc}, []flows.Trigger{flowTrigger}, flow.FlowType().Interrupts(), models.NilStartID, nil)
	if err != nil {
		return fmt.Errorf("error starting flow for contact: %w", err)
	}
	return nil
}
