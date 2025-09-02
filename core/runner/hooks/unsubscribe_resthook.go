package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// UnsubscribeResthook is our hook for when a webhook is called
var UnsubscribeResthook runner.PreCommitHook = &unsubscribeResthook{}

type unsubscribeResthook struct{}

func (h *unsubscribeResthook) Order() int { return 10 }

func (h *unsubscribeResthook) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scene map[*runner.Scene][]any) error {
	// gather all our unsubscribes
	unsubs := make([]*models.ResthookUnsubscribe, 0, len(scene))
	for _, args := range scene {
		for _, u := range args {
			unsubs = append(unsubs, u.(*models.ResthookUnsubscribe))
		}
	}

	if err := models.UnsubscribeResthooks(ctx, tx, unsubs); err != nil {
		return fmt.Errorf("error unsubscribing from resthooks: %w", err)
	}

	return nil
}
