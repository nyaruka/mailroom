package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InsertRuns is our hook for inserting new runs
var InsertRuns runner.PreCommitHook = &insertRuns{}

type insertRuns struct{}

func (h *insertRuns) Order() int { return 1 }

func (h *insertRuns) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	runs := make([]*models.FlowRun, 0, len(scenes))

	for _, items := range scenes {
		for _, item := range items {
			runs = append(runs, item.([]*models.FlowRun)...)
		}
	}

	if err := models.InsertRuns(ctx, tx, runs); err != nil {
		return fmt.Errorf("error inserting runs: %w", err)
	}

	return nil
}
