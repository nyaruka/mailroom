package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeError, handleError)
}

func handleError(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.Error)

	log := slog.With(
		"org", oa.OrgID(), "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "text", event.Text,
	)
	if step := event.Step(); step != nil {
		log = log.With("flow", step.Flow.UUID, "node", step.Node)
	}

	// exceeding the expression cost budget should be effectively impossible, so if it ever happens we want to know
	// about it rather than let it silently degrade a message - error level is fanned out to sentry
	if event.Code == events.ErrorCodeExpressionTooComplex {
		log.Error("expression exceeded cost budget", "expression", event.Extra["expression"])
	} else {
		log.Debug("error event", "code", event.Code)
	}

	return nil
}
