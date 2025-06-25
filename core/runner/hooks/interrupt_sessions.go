package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InterruptSessions is our hook for interrupting existing sessions
var InterruptSessions runner.PreCommitHook = &interruptSessions{}

type interruptSessions struct{}

func (h *interruptSessions) Order() int { return 1 }

func (h *interruptSessions) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	interruptIDs := make([]models.ContactID, 0, len(scenes))
	for s := range scenes {
		interruptIDs = append(interruptIDs, s.ContactID())
	}

	if err := models.InterruptSessionsForContactsTx(ctx, tx, interruptIDs); err != nil {
		return fmt.Errorf("error interrupting contacts: %w", err)
	}

	return nil
}
