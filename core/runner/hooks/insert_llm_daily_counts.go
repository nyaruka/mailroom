package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

var InsertLLMDailyCounts runner.PreCommitHook = &insertLLMDailyCounts{}

type insertLLMDailyCounts struct{}

func (h *insertLLMDailyCounts) Order() int { return 10 }

func (h *insertLLMDailyCounts) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	type key struct {
		LLMID models.LLMID
		Day   dates.Date
		Scope string
	}
	sums := make(map[key]int64)

	for _, args := range scenes {
		for _, a := range args {
			for _, c := range a.([]*models.LLMDailyCount) {
				sums[key{c.LLMID, c.Day, c.Scope}] += c.Count
			}
		}
	}

	counts := make([]*models.LLMDailyCount, 0, len(sums))
	for k, v := range sums {
		counts = append(counts, &models.LLMDailyCount{LLMID: k.LLMID, Day: k.Day, Scope: k.Scope, Count: v})
	}

	if err := models.InsertLLMDailyCounts(ctx, tx, counts); err != nil {
		return fmt.Errorf("error inserting llm daily counts: %w", err)
	}
	return nil
}
