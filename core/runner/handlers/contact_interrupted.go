package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(runner.TypeContactInterrupted, handleContactInterrupted)
}

func handleContactInterrupted(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*runner.ContactInterruptedEvent)

	slog.Debug("contact interrupted", "contact", scene.ContactUUID())

	// interruption takes the contact out of their current flow (the pre-commit hook below clears it in the database)
	// so subscribers should see that change
	if scene.DBContact.CurrentFlowID() != models.NilFlowID {
		scene.DBContact.SetCurrentFlowID(models.NilFlowID)

		if err := scene.AddEvent(ctx, rt, oa, events.NewContactFlowChanged(nil), models.NilUserID, ""); err != nil {
			return fmt.Errorf("error adding contact flow changed event: %w", err)
		}
	}

	scene.AttachPreCommitHook(hooks.InterruptContacts, event)
	scene.AttachPreCommitHook(hooks.UpdateContactModifiedOn, event)
	scene.AttachPostCommitHook(hooks.IndexContacts, event)

	return nil
}
