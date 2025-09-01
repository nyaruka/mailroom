package hooks

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

var InsertNotifications runner.PreCommitHook = &insertNotifications{}

type insertNotifications struct{}

func (h *insertNotifications) Order() int { return 10 }

func (h *insertNotifications) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// de-dupe notifications by user, type and scope
	notifications := make(map[string]*models.Notification)
	for _, es := range scenes {
		for _, e := range es {
			n := e.(*models.Notification)
			notifications[fmt.Sprintf("%d|%s|%s", n.UserID, n.Type, n.Scope)] = n
		}
	}

	if err := models.InsertNotifications(ctx, tx, slices.Collect(maps.Values(notifications))); err != nil {
		return fmt.Errorf("error inserting notifications: %w", err)
	}

	return nil
}
