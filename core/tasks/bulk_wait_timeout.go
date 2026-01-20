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

// TypeBulkWaitTimeout is the type of the task
const TypeBulkWaitTimeout = "bulk_wait_timeout"

func init() {
	RegisterType(TypeBulkWaitTimeout, func() Task { return &BulkWaitTimeout{} })
}

type WaitTimeout struct {
	ContactID   models.ContactID  `json:"contact_id"`
	SessionUUID flows.SessionUUID `json:"session_uuid"`
	SprintUUID  flows.SprintUUID  `json:"sprint_uuid"`
}

// BulkWaitTimeout is the payload of the task
type BulkWaitTimeout struct {
	Timeouts []*WaitTimeout `json:"timeouts"`
}

func (t *BulkWaitTimeout) Type() string {
	return TypeBulkWaitTimeout
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkWaitTimeout) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkWaitTimeout) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkWaitTimeout) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	for _, e := range t.Timeouts {
		err := QueueContact(ctx, rt, oa.OrgID(), e.ContactID, &ctasks.WaitTimeout{SessionUUID: e.SessionUUID, SprintUUID: e.SprintUUID})
		if err != nil {
			return fmt.Errorf("error queuing handle task for wait timeout on session %s: %w", e.SessionUUID, err)
		}
	}

	return nil
}
