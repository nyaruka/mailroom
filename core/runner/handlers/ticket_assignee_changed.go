package handlers

import (
	"context"
	"fmt"
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

	dbTicket, ticket := scene.FindTicket(event.TicketUUID)
	if ticket == nil {
		return nil
	}

	var assignee *models.User
	var assigneeID models.UserID
	if event.Assignee != nil {
		assignee = oa.UserByUUID(event.Assignee.UUID)
		if assignee != nil {
			assigneeID = assignee.ID()
		}
	}

	dbTicket.AssigneeID = assigneeID

	scene.AttachPreCommitHook(hooks.UpdateTickets, dbTicket)
	scene.AttachPreCommitHook(hooks.InsertLegacyTicketEvents, models.NewTicketAssignedEvent(event.UUID(), dbTicket, userID, assigneeID))

	// notify ticket assignee if they didn't self-assign
	if dbTicket.AssigneeID != models.NilUserID && dbTicket.AssigneeID != userID {
		scene.AttachPreCommitHook(hooks.InsertNotifications, models.NewTicketActivityNotification(oa.OrgID(), dbTicket.AssigneeID))
	}

	// if this is an initial assignment record count for user
	if event.Previous == nil && assignee != nil {
		teamID := models.NilTeamID
		if assignee.Team() != nil {
			teamID = assignee.Team().ID
		}

		scene.AttachPreCommitHook(hooks.InsertDailyCounts, map[string]int{
			fmt.Sprintf("tickets:assigned:%d:%d", teamID, assignee.ID()): 1,
		})
	}

	return nil
}
