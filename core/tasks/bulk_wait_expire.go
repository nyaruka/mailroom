package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeBulkWaitExpire is the type of the task
const TypeBulkWaitExpire = "bulk_wait_expire"

func init() {
	RegisterType(TypeBulkWaitExpire, func() Task { return &BulkWaitExpire{} })
}

type WaitExpiration struct {
	ContactID   models.ContactID  `json:"contact_id"`
	SessionUUID flows.SessionUUID `json:"session_uuid"`
	SprintUUID  flows.SprintUUID  `json:"sprint_uuid"`
}

// BulkWaitExpire is the payload of the task
type BulkWaitExpire struct {
	Expirations []*WaitExpiration `json:"expirations"`
}

func (t *BulkWaitExpire) Type() string {
	return TypeBulkWaitExpire
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkWaitExpire) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkWaitExpire) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkWaitExpire) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	for _, e := range t.Expirations {
		err := QueueContact(ctx, rt, oa.OrgID(), e.ContactID, &ctasks.WaitExpired{SessionUUID: e.SessionUUID, SprintUUID: e.SprintUUID})
		if err != nil {
			return fmt.Errorf("error queuing handle task for wait expiration on session %s: %w", e.SessionUUID, err)
		}
	}

	return nil
}
