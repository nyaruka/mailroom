package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeTicketNoteAdded, handleTicketNoteAdded)
}

func handleTicketNoteAdded(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.TicketNoteAdded)

	slog.Debug("ticket note added", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "ticket", event.TicketUUID)

	ticket := scene.FindTicket(event.TicketUUID)
	if ticket == nil {
		return nil
	}

	scene.AttachPreCommitHook(hooks.UpdateTicketActivity, ticket)
	scene.AttachPreCommitHook(hooks.InsertLegacyTicketEvents, hooks.TicketAndEvent{
		Ticket: ticket,
		Event:  models.NewTicketNoteAddedEvent(event.UUID(), ticket, userID, event.Note),
	})

	return nil
}
