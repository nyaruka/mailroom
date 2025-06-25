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

var warningsLogs = map[string]string{
	"deprecated context value accessed: legacy_extra":                                                "", // currently too many to do anything about
	"deprecated context value accessed: webhook recreated from extra":                                "webhook recreated from extra usage",
	"deprecated context value accessed: result.values: use value instead":                            "result.values usage",
	"deprecated context value accessed: result.categories: use category instead":                     "result.categories usage",
	"deprecated context value accessed: result.categories_localized: use category_localized instead": "result.categories_localized usage",
}

func init() {
	runner.RegisterEventHandler(events.TypeWarning, handleWarning)
}

func handleWarning(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event) error {
	event := e.(*events.Warning)

	flow, _ := scene.LocateEvent(e)
	logMsg := warningsLogs[event.Text]
	if logMsg != "" {
		slog.Error(logMsg, "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "text", event.Text, slog.Group("flow", "uuid", flow.UUID, "name", flow.Name))
	}

	return nil
}
