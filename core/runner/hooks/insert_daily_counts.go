package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

var InsertDailyCounts runner.PreCommitHook = &insertDailyCounts{}

type insertDailyCounts struct{}

func (h *insertDailyCounts) Order() int { return 10 }

func (h *insertDailyCounts) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	counts := make(map[string]int)

	for _, args := range scenes {
		for _, dc := range args {
			for k, v := range dc.(map[string]int) {
				counts[k] += v
			}
		}
	}

	if err := models.InsertDailyCounts(ctx, tx, oa, dates.Now(), counts); err != nil {
		return fmt.Errorf("error inserting daily counts: %w", err)
	}

	return nil
}
