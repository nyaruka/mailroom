package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeContactRefreshed, noopHandler)
	runner.RegisterEventHandler(events.TypeEnvironmentRefreshed, noopHandler)
	runner.RegisterEventHandler(events.TypeError, noopHandler)
	runner.RegisterEventHandler(events.TypeFailure, noopHandler)
	runner.RegisterEventHandler(events.TypeMsgWait, noopHandler)
	runner.RegisterEventHandler(events.TypeRunExpired, noopHandler)
	runner.RegisterEventHandler(events.TypeRunResultChanged, noopHandler)
	runner.RegisterEventHandler(events.TypeServiceCalled, noopHandler)
	runner.RegisterEventHandler(events.TypeWaitExpired, noopHandler)
	runner.RegisterEventHandler(events.TypeWaitTimedOut, noopHandler)
	runner.RegisterEventHandler(events.TypeDialWait, noopHandler)
	runner.RegisterEventHandler(events.TypeDialEnded, noopHandler)
}

// our hook for events we ignore in a run
func noopHandler(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, event flows.Event) error {
	slog.Debug("noop event", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "type", event.Type())

	return nil
}
