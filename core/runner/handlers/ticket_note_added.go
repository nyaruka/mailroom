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
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeTicketNoteAdded, handleTicketNoteAdded)
}

func handleTicketNoteAdded(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.TicketNoteAdded)

	slog.Debug("ticket note added", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "ticket", event.TicketUUID)

	dbTicket, ticket := scene.FindTicket(event.TicketUUID)
	if ticket == nil {
		return nil
	}

	dbTicket.LastActivityOn = dates.Now()

	scene.AttachPreCommitHook(hooks.UpdateTickets, dbTicket)
	scene.AttachPreCommitHook(hooks.InsertLegacyTicketEvents, models.NewTicketNoteAddedEvent(event.UUID(), dbTicket, userID, event.Note))

	// notify ticket assignee if they didn't add note themselves
	if dbTicket.AssigneeID != models.NilUserID && dbTicket.AssigneeID != userID {
		scene.AttachPreCommitHook(hooks.InsertNotifications, models.NewTicketActivityNotification(oa.OrgID(), dbTicket.AssigneeID))
	}

	return nil
}
