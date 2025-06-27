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
	runner.RegisterEventHandler(events.TypeBroadcastCreated, handleBroadcastCreated)
}

func handleBroadcastCreated(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.BroadcastCreated)

	slog.Debug("broadcast created", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "translations", event.Translations[event.BaseLanguage])

	scene.AttachPostCommitHook(hooks.CreateBroadcasts, event)

	return nil
}
