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
	runner.RegisterEventHandler(events.TypeIVRCreated, handleIVRCreated)
}

// handleIVRCreated creates the db msg for the passed in event
func handleIVRCreated(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event) error {
	event := e.(*events.IVRCreatedEvent)

	slog.Debug("ivr created", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "text", event.Msg.Text())

	// get our call
	if scene.Call == nil {
		return fmt.Errorf("ivr session must have a call set")
	}

	// if our call is no longer in progress, return
	if scene.Call.Status() != models.CallStatusWired && scene.Call.Status() != models.CallStatusInProgress {
		return nil
	}

	msg := models.NewOutgoingIVR(rt.Config, oa.OrgID(), scene.Call, event.Msg, event.CreatedOn())

	// register to have this message committed
	scene.AttachPreCommitHook(hooks.InsertIVRMessages, msg)

	return nil
}
