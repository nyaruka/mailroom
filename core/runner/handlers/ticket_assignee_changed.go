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
	runner.RegisterEventHandler(events.TypeTicketAssigneeChanged, handleTicketAssigneeChanged)
}

func handleTicketAssigneeChanged(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.TicketAssigneeChanged)

	slog.Debug("ticket assignee changed", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "ticket", event.TicketUUID)

	ticket := scene.FindTicket(event.TicketUUID)
	if ticket == nil {
		return nil
	}

	assigneeID := models.NilUserID
	if event.Assignee != nil {
		if assignee := oa.UserByUUID(event.Assignee.UUID); assignee != nil {
			assigneeID = assignee.ID()
		}
	}

	scene.AttachPreCommitHook(hooks.UpdateTicketTopic, hooks.TicketAssigneeUpdate{Ticket: ticket, AssigneeID: assigneeID, UserID: userID})
	scene.AttachPreCommitHook(hooks.InsertLegacyTicketEvents, models.NewTicketAssignedEvent(event.UUID(), ticket, userID, assigneeID))

	// notify ticket assignee if they didn't self-assign
	if ticket.AssigneeID != models.NilUserID && ticket.AssigneeID != userID {
		scene.AttachPreCommitHook(hooks.InsertNotifications, models.NewTicketActivityNotification(oa.OrgID(), ticket.AssigneeID))
	}

	return nil
}
