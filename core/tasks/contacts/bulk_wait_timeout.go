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

// TypeBulkWaitTimeout is the type of the task
const TypeBulkWaitTimeout = "bulk_wait_timeout"

func init() {
	tasks.RegisterType(TypeBulkWaitTimeout, func() tasks.Task { return &BulkWaitTimeoutTask{} })
}

type WaitTimeout struct {
	ContactID   models.ContactID  `json:"contact_id"`
	SessionUUID flows.SessionUUID `json:"session_uuid"`
	SprintUUID  flows.SprintUUID  `json:"sprint_uuid"`
}

// BulkWaitTimeoutTask is the payload of the task
type BulkWaitTimeoutTask struct {
	Timeouts []*WaitTimeout `json:"timeouts"`
}

func (t *BulkWaitTimeoutTask) Type() string {
	return TypeBulkWaitTimeout
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkWaitTimeoutTask) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkWaitTimeoutTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkWaitTimeoutTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	rc := rt.VK.Get()
	defer rc.Close()

	for _, e := range t.Timeouts {
		err := handler.QueueTask(rc, oa.OrgID(), e.ContactID, &ctasks.WaitTimeoutTask{SessionUUID: e.SessionUUID, SprintUUID: e.SprintUUID})
		if err != nil {
			return fmt.Errorf("error queuing handle task for wait timeout on session %s: %w", e.SessionUUID, err)
		}
	}

	return nil
}
