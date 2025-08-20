package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	runner.RegisterEventHandler(runner.TypeSessionInterrupted, handleSessionInterrupted)
}

func handleSessionInterrupted(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*runner.SessionInterruptedEvent)

	slog.Debug("session interrupted", "contact", scene.ContactUUID(), "session", event.SessionUUID)

	scene.AttachPreCommitHook(hooks.InterruptSessions, event)

	return nil
}
