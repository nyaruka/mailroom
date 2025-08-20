package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InteruptRuns is our hook for interrupting runs from prior sessions
var InterruptRuns runner.PreCommitHook = &interruptRuns{}

type interruptRuns struct{}

func (h *interruptRuns) Order() int { return 0 } // run before everything else

func (h *interruptRuns) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	runUUIDs := make([]flows.RunUUID, 0, len(scenes))
	for _, es := range scenes {
		runUUIDs = append(runUUIDs, es[0].(*events.RunEnded).RunUUID)
	}

	if len(runUUIDs) > 0 {
		if err := models.InterruptRuns(ctx, tx, runUUIDs); err != nil {
			return fmt.Errorf("error interrupting runs: %w", err)
		}
	}

	return nil
}
