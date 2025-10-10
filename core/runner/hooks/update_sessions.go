package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// UpdateSessions is our hook for updating existing sessions
var UpdateSessions runner.PreCommitHook = &updateSessions{}

type updateSessions struct{}

func (h *updateSessions) Order() int { return 2 }

func (h *updateSessions) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// TODO if we ever support bulk resumes this should be optimized to do a single update for all sessions
	for scene := range scenes {
		if err := scene.DBSession.Update(ctx, rt, tx, oa, scene.Session, scene.Sprint, scene.DBContact); err != nil {
			return fmt.Errorf("error updating session: %w", err)
		}
	}

	return nil
}
