package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(runner.TypeContactCreated, handleContactCreated)
}

// handleContactCreated ensures newly created contacts are indexed - they might not generate any other events which
// would do that, e.g. an imported contact with only URNs
func handleContactCreated(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*runner.ContactCreatedEvent)

	slog.Debug("contact created", "contact", scene.ContactUUID())

	scene.AttachPostCommitHook(hooks.IndexContacts, event)

	return nil
}
