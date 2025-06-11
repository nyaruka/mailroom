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
	runner.RegisterEventHandler(events.TypeMsgReceived, handleMsgReceived)
}

// handleMsgReceived takes care of update last seen on and any campaigns based on that
func handleMsgReceived(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event) error {
	event := e.(*events.MsgReceivedEvent)

	slog.Debug("msg received", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "text", event.Msg.Text(), "urn", event.Msg.URN())

	// update the message to be handled
	if scene.IncomingMsg != nil {
		scene.AttachPreCommitHook(hooks.UpdateMessageHandled, event)
	}

	// update the contact's last seen date
	scene.AttachPreCommitHook(hooks.UpdateContactLastSeenOn, event)
	scene.AttachPreCommitHook(hooks.UpdateCampaignEvents, event)

	return nil
}
