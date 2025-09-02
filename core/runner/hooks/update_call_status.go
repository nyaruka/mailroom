package hooks

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// UpdateCallStatus is our hook for updating IVR call status
var UpdateCallStatus runner.PreCommitHook = &updateCallStatus{}

type updateCallStatus struct{}

func (h *updateCallStatus) Order() int { return 10 }

func (h *updateCallStatus) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for scene, args := range scenes {
		status := args[len(args)-1].(models.CallStatus)

		if status == models.CallStatusInProgress {
			session := scene.Session

			if err := scene.DBCall.SetInProgress(ctx, rt.DB, session.UUID(), session.CreatedOn()); err != nil {
				return fmt.Errorf("error updating call to in progress: %w", err)
			}
		} else {
			if err := scene.DBCall.UpdateStatus(ctx, tx, status, 0, time.Now()); err != nil {
				return fmt.Errorf("error updating call status: %w", err)
			}
		}
	}

	return nil
}
