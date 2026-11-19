package hooks

import (
	"context"
	"log/slog"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// IncrementFlowMsgTallies is our hook for tallying incoming messages handled by flows. The flow is only known once the
// scene's sprint has been added, which is after the message's event has been handled.
var IncrementFlowMsgTallies runner.PostCommitHook = &incrementFlowMsgTallies{}

type incrementFlowMsgTallies struct{}

func (h *incrementFlowMsgTallies) Order() int { return 10 }

func (h *incrementFlowMsgTallies) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	amounts := make(map[string]int64)
	now := dates.Now()

	for scene := range scenes {
		if flow := HandlingFlow(scene); flow != nil {
			for field, amount := range models.FlowMsgsInTallies(flow.ID(), now) {
				amounts[field] += amount
			}
		}
	}

	if len(amounts) == 0 {
		return nil
	}

	vc := rt.VK.Get()
	defer vc.Close()

	if err := rt.Tallies.Increment(ctx, vc, amounts); err != nil {
		slog.Error("error incrementing flow msg tallies", "error", err, "org_id", oa.OrgID())
	}

	return nil
}
