package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeBulkSessionExpire is the type of the task
const TypeBulkSessionExpire = "bulk_session_expire"

func init() {
	RegisterType(TypeBulkSessionExpire, func() Task { return &BulkSessionExpire{} })
}

// BulkSessionExpire is the payload of the task
type BulkSessionExpire struct {
	SessionUUIDs []flows.SessionUUID `json:"session_uuids"`
}

func (t *BulkSessionExpire) Type() string {
	return TypeBulkSessionExpire
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkSessionExpire) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkSessionExpire) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkSessionExpire) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	if err := models.ExitSessions(ctx, rt.DB, t.SessionUUIDs, models.SessionStatusExpired); err != nil {
		return fmt.Errorf("error bulk expiring sessions: %w", err)
	}
	return nil
}
