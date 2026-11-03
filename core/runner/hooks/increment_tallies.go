package hooks

import (
	"context"
	"log/slog"

	"github.com/nyaruka/gocommon/tallies"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// IncrementTallies is our hook for incrementing tallies once the changes they count are committed
var IncrementTallies runner.PostCommitHook = &incrementTallies{}

type incrementTallies struct{}

func (h *incrementTallies) Order() int { return 10 }

func (h *incrementTallies) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	amounts := make(map[tallies.Key]int64)
	for _, args := range scenes {
		for _, a := range args {
			for key, amount := range a.(map[tallies.Key]int64) {
				amounts[key] += amount
			}
		}
	}

	vc := rt.VK.Get()
	defer vc.Close()

	if err := models.Tallies.Increment(ctx, vc, tallies.OwnerID(oa.OrgID()), amounts); err != nil {
		slog.Error("error incrementing tallies", "error", err, "org_id", oa.OrgID())
	}

	return nil
}
