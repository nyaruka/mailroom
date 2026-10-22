package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// TypeBulkTicketInactive is the type of the task
const TypeBulkTicketInactive = "bulk_ticket_inactive"

func init() {
	RegisterType(TypeBulkTicketInactive, func() Task { return &BulkTicketInactive{} })
}

type InactiveTicketRef struct {
	ContactID  models.ContactID `json:"contact_id"`
	TicketUUID core.TicketUUID  `json:"ticket_uuid"`
}

// BulkTicketInactive is the payload of the task
type BulkTicketInactive struct {
	Tickets []*InactiveTicketRef `json:"tickets"`
}

func (t *BulkTicketInactive) Type() string {
	return TypeBulkTicketInactive
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkTicketInactive) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkTicketInactive) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkTicketInactive) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error {
	for _, r := range t.Tickets {
		err := QueueContact(ctx, rt, oa.OrgID(), r.ContactID, &ctasks.TicketInactive{TicketUUID: r.TicketUUID})
		if err != nil {
			return fmt.Errorf("error queuing handle task for inactive ticket %s: %w", r.TicketUUID, err)
		}
	}

	return nil
}
