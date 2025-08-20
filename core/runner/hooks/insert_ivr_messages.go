package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InsertIVRMessages is our hook for comitting scene messages / say commands
var InsertIVRMessages runner.PreCommitHook = &insertIVRMessages{}

type insertIVRMessages struct{}

func (h *insertIVRMessages) Order() int { return 10 }

func (h *insertIVRMessages) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	msgs := make([]*models.Msg, 0, len(scenes))
	for _, s := range scenes {
		for _, m := range s {
			msgs = append(msgs, m.(*models.Msg))
		}
	}

	if err := models.InsertMessages(ctx, tx, msgs); err != nil {
		return fmt.Errorf("error writing messages: %w", err)
	}

	return nil
}
