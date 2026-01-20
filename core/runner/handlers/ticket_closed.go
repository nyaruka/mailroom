package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeTicketClosed, handleTicketClosed)
}

func handleTicketClosed(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.TicketClosed)

	slog.Debug("ticket closed", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "ticket", event.TicketUUID)

	dbTicket := scene.DBContact.FindTicket(event.TicketUUID)
	dbTicket.Status = models.TicketStatusClosed
	dbTicket.ClosedOn = &event.CreatedOn_
	dbTicket.LastActivityOn = dates.Now()

	scene.AttachPreCommitHook(hooks.UpdateTickets, dbTicket)
	scene.AttachPostCommitHook(hooks.QueueContactTask, ctasks.NewTicketClosed(event))

	return nil
}
