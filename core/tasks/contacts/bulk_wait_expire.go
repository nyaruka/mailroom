package contacts

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeBulkWaitExpire is the type of the task
const TypeBulkWaitExpire = "bulk_wait_expire"

func init() {
	tasks.RegisterType(TypeBulkWaitExpire, func() tasks.Task { return &BulkWaitExpireTask{} })
}

type WaitExpiration struct {
	ContactID   models.ContactID  `json:"contact_id"`
	SessionUUID flows.SessionUUID `json:"session_uuid"`
	SprintUUID  flows.SprintUUID  `json:"sprint_uuid"`
}

// BulkWaitExpireTask is the payload of the task
type BulkWaitExpireTask struct {
	Expirations []*WaitExpiration `json:"expirations"`
}

func (t *BulkWaitExpireTask) Type() string {
	return TypeBulkWaitExpire
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkWaitExpireTask) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkWaitExpireTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkWaitExpireTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	rc := rt.VK.Get()
	defer rc.Close()

	for _, e := range t.Expirations {
		err := handler.QueueTask(rc, oa.OrgID(), e.ContactID, &ctasks.WaitExpiredTask{SessionUUID: e.SessionUUID, SprintUUID: e.SprintUUID})
		if err != nil {
			return fmt.Errorf("error queuing handle task for wait expiration on session %s: %w", e.SessionUUID, err)
		}
	}

	return nil
}
