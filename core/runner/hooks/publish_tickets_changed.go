package hooks

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// PublishTicketsChanged is our hook for telling the workspace's open ticket lists which of its tickets have changed.
// It runs once per committed batch, so a batch touching any number of tickets costs a single publish.
var PublishTicketsChanged runner.PostCommitHook = &publishTicketsChanged{}

type publishTicketsChanged struct{}

func (h *publishTicketsChanged) Order() int { return 10 }

func (h *publishTicketsChanged) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	seen := make(map[core.TicketUUID]bool, len(scenes))
	for _, args := range scenes {
		for _, arg := range args {
			seen[arg.(core.TicketUUID)] = true
		}
	}

	ticketUUIDs := slices.Sorted(maps.Keys(seen)) // for test determinism

	// realtime delivery is best-effort - a publish failure shouldn't fail processing of already committed scenes
	if err := models.PublishTicketsChanged(ctx, rt, oa, ticketUUIDs); err != nil {
		slog.Error("error publishing tickets changed", "error", err, "org_id", oa.OrgID())
	}

	return nil
}
