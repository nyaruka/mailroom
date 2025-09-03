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
	runner.RegisterEventHandler(events.TypeTicketTopicChanged, handleTicketTopicChanged)
}

func handleTicketTopicChanged(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.TicketTopicChanged)

	slog.Debug("ticket topic changed", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "ticket", event.TicketUUID)

	ticket := scene.FindTicket(event.TicketUUID)
	topic := oa.TopicByUUID(event.Topic.UUID)
	if ticket == nil || topic == nil {
		return nil
	}

	scene.AttachPreCommitHook(hooks.UpdateTickets, ticket)
	scene.AttachPreCommitHook(hooks.InsertLegacyTicketEvents, models.NewTicketTopicChangedEvent(event.UUID(), ticket, userID, topic.ID()))

	return nil
}
