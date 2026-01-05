package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// InsertSessions is our hook for inserting new sessions
var InsertSessions runner.PreCommitHook = &insertSessions{}

type insertSessions struct{}

func (h *insertSessions) Order() int { return 1 } // after interrupts

func (h *insertSessions) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	sessions := make([]*models.Session, 0, len(scenes))

	for _, args := range scenes {
		sessions = append(sessions, args[0].(*models.Session))
	}

	if err := models.InsertSessions(ctx, tx, sessions); err != nil {
		return fmt.Errorf("error inserting sessions: %w", err)
	}

	return nil
}
