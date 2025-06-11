package contacts

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeBulkSessionExpire is the type of the task
const TypeBulkSessionExpire = "bulk_session_expire"

func init() {
	tasks.RegisterType(TypeBulkSessionExpire, func() tasks.Task { return &BulkSessionExpireTask{} })
}

// BulkSessionExpireTask is the payload of the task
type BulkSessionExpireTask struct {
	SessionUUIDs []flows.SessionUUID `json:"session_uuids"`
}

func (t *BulkSessionExpireTask) Type() string {
	return TypeBulkSessionExpire
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkSessionExpireTask) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkSessionExpireTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkSessionExpireTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	if err := models.ExitSessions(ctx, rt.DB, t.SessionUUIDs, models.SessionStatusExpired); err != nil {
		return fmt.Errorf("error bulk expiring sessions: %w", err)
	}
	return nil
}
