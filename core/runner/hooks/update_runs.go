package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// UpdateRuns is our hook for updating existing runs
var UpdateRuns runner.PreCommitHook = &updateRuns{}

type updateRuns struct{}

func (h *updateRuns) Order() int { return 10 }

func (h *updateRuns) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	runs := make([]*models.FlowRun, 0, len(scenes))

	for _, args := range scenes {
		for _, item := range args {
			runs = append(runs, item.([]*models.FlowRun)...)
		}
	}

	if err := models.UpdateRuns(ctx, tx, runs); err != nil {
		return fmt.Errorf("error updating runs: %w", err)
	}

	return nil
}
