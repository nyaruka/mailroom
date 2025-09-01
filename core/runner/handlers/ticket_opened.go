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
	runner.RegisterEventHandler(events.TypeTicketOpened, handleTicketOpened)
}

// handleTicketOpened is called for each ticket opened event
func handleTicketOpened(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.TicketOpened)

	slog.Debug("ticket opened", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "ticket", event.Ticket.UUID)

	var topicID models.TopicID
	if event.Ticket.Topic != nil {
		topic := oa.TopicByUUID(event.Ticket.Topic.UUID)
		if topic == nil {
			return fmt.Errorf("unable to find topic with UUID: %s", event.Ticket.Topic.UUID)
		}
		topicID = topic.ID()
	}

	var assigneeID models.UserID
	if event.Ticket.Assignee != nil {
		assignee := oa.UserByUUID(event.Ticket.Assignee.UUID)
		if assignee == nil {
			return fmt.Errorf("unable to find user with UUID: %s", event.Ticket.Assignee.UUID)
		}
		assigneeID = assignee.ID()
	}

	var openedInID models.FlowID
	if scene.Session != nil {
		flow, _ := scene.LocateEvent(e)
		openedInID = flow.ID()
	}

	ticket := models.NewTicket(
		event.Ticket.UUID,
		oa.OrgID(),
		userID,
		openedInID,
		scene.ContactID(),
		topicID,
		assigneeID,
	)

	scene.AttachPreCommitHook(hooks.InsertTickets, hooks.TicketAndNote{Event: event, Ticket: ticket})

	if assigneeID == models.NilUserID {
		// ticket is unassigned so notify all possible assignees except the user who opened the ticket
		for _, user := range models.GetTicketAssignableUsers(oa) {
			if userID != user.ID() {
				scene.AttachPreCommitHook(hooks.InsertNotifications, models.NewTicketsOpenedNotification(oa.OrgID(), user.ID()))
			}
		}
	} else if assigneeID != userID {
		// ticket is assigned so just notify the assignee if they didn't self-assign
		scene.AttachPreCommitHook(hooks.InsertNotifications, models.NewTicketActivityNotification(oa.OrgID(), assigneeID))
	}

	return nil
}
