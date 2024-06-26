package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"

	"github.com/jmoiron/sqlx"
)

// UnsubscribeResthookHook is our hook for when a webhook is called
var UnsubscribeResthookHook models.EventCommitHook = &unsubscribeResthookHook{}

type unsubscribeResthookHook struct{}

// Apply squashes and applies all our resthook unsubscriptions
func (h *unsubscribeResthookHook) Apply(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scene map[*models.Scene][]any) error {
	// gather all our unsubscribes
	unsubs := make([]*models.ResthookUnsubscribe, 0, len(scene))
	for _, us := range scene {
		for _, u := range us {
			unsubs = append(unsubs, u.(*models.ResthookUnsubscribe))
		}
	}

	err := models.UnsubscribeResthooks(ctx, tx, unsubs)
	if err != nil {
		return fmt.Errorf("error unsubscribing from resthooks: %w", err)
	}

	return nil
}
