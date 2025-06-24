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

func (h *updateCallStatus) Order() int { return 1 }

func (h *updateCallStatus) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for scene, es := range scenes {
		status := es[len(es)-1].(models.CallStatus)

		if status == models.CallStatusInProgress {
			session := scene.Session

			if err := scene.Call.SetInProgress(ctx, rt.DB, session.UUID(), session.CreatedOn()); err != nil {
				return fmt.Errorf("error updating call to in progress: %w", err)
			}
		} else {
			if err := scene.Call.UpdateStatus(ctx, tx, status, 0, time.Now()); err != nil {
				return fmt.Errorf("error updating call status: %w", err)
			}
		}
	}

	return nil
}
