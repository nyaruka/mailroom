package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// AddMessageLabels is our hook for input labels being added
var AddMessageLabels runner.PreCommitHook = &addMessageLabels{}

type addMessageLabels struct{}

func (h *addMessageLabels) Order() int { return 10 }

func (h *addMessageLabels) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// build our list of msg label adds, we dedupe these so we never double add in the same transaction
	seen := make(map[string]bool)
	adds := make([]*models.MsgLabelAdd, 0, len(scenes))

	for _, args := range scenes {
		for _, a := range args {
			add := a.(*models.MsgLabelAdd)
			key := fmt.Sprintf("%d:%d", add.LabelID, add.MsgID)
			if !seen[key] {
				adds = append(adds, add)
				seen[key] = true
			}
		}
	}

	if err := models.AddMsgLabels(ctx, tx, adds); err != nil {
		return fmt.Errorf("error adding message labels: %w", err)
	}

	return nil
}
