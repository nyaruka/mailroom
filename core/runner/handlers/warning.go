package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const (
	deprecatedUsagesKey = "deprecated_context_usage"
)

func init() {
	runner.RegisterEventHandler(events.TypeWarning, handleWarning)
}

func handleWarning(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.Warning)

	// deprecated context warnings always come from the engine so will have a step, but check anyway
	if event.Code == events.WarningCodeDeprecatedContext && event.Step() != nil {
		// text is like "@contact.id is deprecated, use @contact.ref instead"
		ref, _, _ := strings.Cut(event.Text, " ")
		ref = strings.TrimPrefix(ref, "@")

		key := fmt.Sprintf("%s/%s", event.Step().Flow.UUID, ref)

		vc := rt.VK.Get()
		defer vc.Close()

		if _, err := vc.Do("HINCRBY", deprecatedUsagesKey, key, 1); err != nil {
			return fmt.Errorf("error recording deprecated context usage: %w", err)
		}
	}

	return nil
}
