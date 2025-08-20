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
	runner.RegisterEventHandler(events.TypeRunEnded, handleRunEnded)
}

func handleRunEnded(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.RunEnded)

	slog.Debug("run ended", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "run", event.RunUUID, "status", event.Status)

	if event.Status == flows.RunStatusInterrupted {
		scene.AttachPreCommitHook(hooks.InterruptRuns, event)
	}

	return nil
}
