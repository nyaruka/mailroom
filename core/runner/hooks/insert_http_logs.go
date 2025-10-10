package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// InsertHTTPLogs is our hook for inserting classifier logs
var InsertHTTPLogs runner.PreCommitHook = &insertHTTPLogs{}

type insertHTTPLogs struct{}

func (h *insertHTTPLogs) Order() int { return 10 }

func (h *insertHTTPLogs) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our logs
	logs := make([]*models.HTTPLog, 0, len(scenes))
	for _, args := range scenes {
		for _, l := range args {
			logs = append(logs, l.(*models.HTTPLog))
		}
	}

	if err := models.InsertHTTPLogs(ctx, tx, logs); err != nil {
		return fmt.Errorf("error inserting http logs: %w", err)
	}

	return nil
}
