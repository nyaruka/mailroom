package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

var InsertNotifications runner.PreCommitHook = &insertNotifications{}

type insertNotifications struct{}

func (h *insertNotifications) Order() int { return 10 }

func (h *insertNotifications) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// de-dupe notifications by user, type and scope, remembering a scene to publish each from
	type kept struct {
		scene *runner.Scene
		n     *models.Notification
	}
	deduped := make(map[string]kept)
	for scene, args := range scenes {
		for _, e := range args {
			n := e.(*models.Notification)
			deduped[fmt.Sprintf("%d|%s|%s", n.UserID, n.Type, n.Scope)] = kept{scene, n}
		}
	}

	notifications := make([]*models.Notification, 0, len(deduped))
	for _, k := range deduped {
		notifications = append(notifications, k.n)
	}

	inserted, err := models.InsertNotifications(ctx, tx, notifications)
	if err != nil {
		return fmt.Errorf("error inserting notifications: %w", err)
	}

	// record each newly created notification on its scene so it gets published once the commit succeeds
	insertedSet := make(map[*models.Notification]bool, len(inserted))
	for _, n := range inserted {
		insertedSet[n] = true
	}
	for _, k := range deduped {
		if insertedSet[k.n] {
			k.scene.AddNotifications(k.n)
		}
	}

	return nil
}
