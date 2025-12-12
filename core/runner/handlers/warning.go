package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	deprecatedContextWarningPrefix = "deprecated context value accessed: "
	deprecatedUsagesKey            = "deprecated_context_usage"
)

func init() {
	runner.RegisterEventHandler(events.TypeWarning, handleWarning)
}

func handleWarning(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.Warning)

	if rem, ok := strings.CutPrefix(event.Text, deprecatedContextWarningPrefix); ok {
		if strings.Contains(rem, ":") {
			rem = rem[:strings.Index(rem, ":")]
		}

		key := fmt.Sprintf("%s/%s", event.Step().Run().Flow().UUID(), rem)

		vc := rt.VK.Get()
		defer vc.Close()

		if _, err := vc.Do("HINCRBY", deprecatedUsagesKey, key, 1); err != nil {
			return fmt.Errorf("error recording deprecated context usage: %w", err)
		}
	}

	return nil
}
