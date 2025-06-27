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
func handleIVRCreated(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.IVRCreated)

	slog.Debug("ivr created", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "text", event.Msg.Text())

	// get our call
	if scene.DBCall == nil {
		return fmt.Errorf("ivr session must have a call set")
	}

	// if our call is no longer in progress, return
	if scene.DBCall.Status() != models.CallStatusWired && scene.DBCall.Status() != models.CallStatusInProgress {
		return nil
	}

	msg := models.NewOutgoingIVR(rt.Config, oa.OrgID(), scene.DBCall, event.Msg, event.CreatedOn())

	// register to have this message committed
	scene.AttachPreCommitHook(hooks.InsertIVRMessages, msg)

	return nil
}
