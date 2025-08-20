package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InteruptSessions is our hook for interrupting existing sessions
var InterruptSessions runner.PreCommitHook = &interruptSessions{}

type interruptSessions struct{}

func (h *interruptSessions) Order() int { return 0 } // run before everything else

func (h *interruptSessions) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	interruptUUIDs := make([]flows.SessionUUID, 0, len(scenes))
	for _, es := range scenes {
		interruptUUIDs = append(interruptUUIDs, es[0].(*runner.SessionInterruptedEvent).SessionUUID)
	}

	if len(interruptUUIDs) > 0 {
		if err := models.InterruptSessions(ctx, tx, interruptUUIDs); err != nil {
			return fmt.Errorf("error interrupting runs: %w", err)
		}
	}

	return nil
}
