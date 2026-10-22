package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeTicketClosed, handleTicketClosed)
}

func handleTicketClosed(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.TicketClosed)

	slog.Debug("ticket closed", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "ticket", event.TicketUUID)

	dbTicket := scene.DBContact.FindTicket(event.TicketUUID)
	dbTicket.Status = models.TicketStatusClosed
	dbTicket.ClosedOn = &event.CreatedOn_

	// automatic closes of inactive tickets aren't activity, and bumping them would put them ahead of tickets recently
	// closed by users
	if userID != models.NilUserID {
		dbTicket.LastActivityOn = dates.Now()
	}

	scene.AttachPreCommitHook(hooks.UpdateTickets, dbTicket)
	scene.AttachPreCommitHook(hooks.UpdateTicketFires, dbTicket)

	// contacts shouldn't have more than one open ticket, but older data can, and if those tickets are closed together
	// (e.g. by an automatic close of inactive tickets) only fire the trigger for the first of them
	for _, prev := range scene.Events() {
		if prev.Type() == events.TypeTicketClosed {
			return nil
		}
	}

	// automatic closes can come in large batches so avoid queuing tasks for contacts that won't match a trigger
	trigger := models.FindMatchingTicketClosedTrigger(oa, scene.Contact)
	if trigger == nil {
		return nil
	}

	// closed ticket triggers should only use background flows, but don't rely on that for automatic closes which could
	// otherwise interrupt or call large numbers of contacts
	if userID == models.NilUserID {
		flow, err := oa.FlowByID(trigger.FlowID())
		if err == models.ErrNotFound {
			return nil
		}
		if err != nil {
			return fmt.Errorf("error loading flow for trigger: %w", err)
		}
		if flow.FlowType() != models.FlowTypeBackground {
			return nil
		}
	}

	scene.AttachPostCommitHook(hooks.QueueContactTask, ctasks.NewTicketClosed(event))

	return nil
}
