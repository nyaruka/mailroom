package handlers

import (
	"context"
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

	// automatic closes can come in large batches so avoid queuing tasks for contacts that won't match a trigger
	if models.FindMatchingTicketClosedTrigger(oa, scene.Contact) != nil {
		scene.AttachPostCommitHook(hooks.QueueContactTask, ctasks.NewTicketClosed(event))
	}

	return nil
}
