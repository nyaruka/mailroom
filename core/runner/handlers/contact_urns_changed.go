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
	runner.RegisterEventHandler(events.TypeContactURNsChanged, handleContactURNsChanged)
}

// handleContactURNsChanged is called for each contact urn changed event that is encountered
func handleContactURNsChanged(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.ContactURNsChanged)

	slog.Debug("contact urns changed", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "urns", event.URNs)

	var flow *models.Flow
	if scene.Session != nil {
		flow, _ = scene.LocateEvent(e)
	}

	// create our URN changed event
	change := &models.ContactURNsChanged{
		ContactID: scene.ContactID(),
		OrgID:     oa.OrgID(),
		URNs:      event.URNs,
		Flow:      flow,
	}

	scene.AttachPreCommitHook(hooks.UpdateContactURNs, change)
	scene.AttachPreCommitHook(hooks.UpdateContactModifiedOn, event)

	return nil
}
