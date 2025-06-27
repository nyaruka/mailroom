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
	runner.RegisterEventHandler(events.TypeResthookCalled, handleResthookCalled)
}

// handleResthookCalled is called for each resthook call in a scene
func handleResthookCalled(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.ResthookCalled)

	slog.Debug("resthook called", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "resthook", event.Resthook)

	// look up our resthook id
	resthook := oa.ResthookBySlug(event.Resthook)
	if resthook == nil {
		slog.Warn("unable to find resthook with slug, ignoring event", "resthook", event.Resthook)
		return nil
	}

	// create an event for this call
	re := models.NewWebhookEvent(
		oa.OrgID(),
		resthook.ID(),
		string(event.Payload),
		event.CreatedOn(),
	)
	scene.AttachPreCommitHook(hooks.InsertWebhookEvent, re)

	return nil
}
