package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InsertWebhookEvent is our hook for when a resthook needs to be inserted
var InsertWebhookEvent runner.PreCommitHook = &insertWebhookEventHook{}

type insertWebhookEventHook struct{}

func (h *insertWebhookEventHook) Order() int { return 1 }

func (h *insertWebhookEventHook) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	events := make([]*models.WebhookEvent, 0, len(scenes))
	for _, rs := range scenes {
		for _, r := range rs {
			events = append(events, r.(*models.WebhookEvent))
		}
	}

	if err := models.InsertWebhookEvents(ctx, tx, events); err != nil {
		return fmt.Errorf("error inserting webhook events: %w", err)
	}

	return nil
}
