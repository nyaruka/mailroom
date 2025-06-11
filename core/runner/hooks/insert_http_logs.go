package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InsertHTTPLogs is our hook for inserting classifier logs
var InsertHTTPLogs runner.PreCommitHook = &insertHTTPLogs{}

type insertHTTPLogs struct{}

func (h *insertHTTPLogs) Order() int { return 1 }

func (h *insertHTTPLogs) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our logs
	logs := make([]*models.HTTPLog, 0, len(scenes))
	for _, ls := range scenes {
		for _, l := range ls {
			logs = append(logs, l.(*models.HTTPLog))
		}
	}

	if err := models.InsertHTTPLogs(ctx, tx, logs); err != nil {
		return fmt.Errorf("error inserting http logs: %w", err)
	}

	return nil
}
