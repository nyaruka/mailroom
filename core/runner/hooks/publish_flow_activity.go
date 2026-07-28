package hooks

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// PublishFlowActivity is our hook for notifying live watchers of flows (e.g. open editors) that activity in those
// flows has changed
var PublishFlowActivity runner.PostCommitHook = &publishFlowActivity{}

type publishFlowActivity struct{}

func (h *publishFlowActivity) Order() int { return 10 }

func (h *publishFlowActivity) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather the distinct flows traversed by the committed sprints - the same segments the insert flow stats hook
	// counts, so a notification goes out exactly when there are new counts to see
	seen := make(map[assets.FlowUUID]bool, 10)
	for scene := range scenes {
		for _, seg := range scene.Sprint.Segments() {
			seen[seg.Flow().Asset().(*models.Flow).UUID()] = true
		}
	}

	flowUUIDs := slices.Sorted(maps.Keys(seen)) // for test determinism

	// realtime delivery is best-effort - a publish failure shouldn't fail processing of already committed scenes
	if err := models.PublishFlowActivity(ctx, rt, flowUUIDs); err != nil {
		slog.Error("error publishing flow activity", "error", err)
	}

	return nil
}
