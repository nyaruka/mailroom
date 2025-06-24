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
func handleTicketOpened(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event) error {
	event := e.(*events.TicketOpenedEvent)

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
		scene.UserID,
		openedInID,
		scene.ContactID(),
		topicID,
		assigneeID,
	)

	scene.AttachPreCommitHook(hooks.InsertTickets, hooks.TicketAndNote{Ticket: ticket, Note: event.Note})

	return nil
}
