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
	runner.RegisterEventHandler(events.TypeContactFieldChanged, handleContactFieldChanged)
}

// handleContactFieldChanged is called when a contact field changes
func handleContactFieldChanged(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event) error {
	event := e.(*events.ContactFieldChangedEvent)

	slog.Debug("contact field changed", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "field", event.Field.Key, "value", event.Value)

	scene.AttachPreCommitHook(hooks.UpdateContactFields, event)
	scene.AttachPreCommitHook(hooks.UpdateCampaignEvents, event)
	scene.AttachPreCommitHook(hooks.UpdateContactModifiedOn, event)

	return nil
}
