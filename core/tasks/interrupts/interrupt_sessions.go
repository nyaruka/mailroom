package interrupts

import (
	"context"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeInterruptSessions is the type of the interrupt session task
const TypeInterruptSessions = "interrupt_sessions"

func init() {
	tasks.RegisterType(TypeInterruptSessions, func() tasks.Task { return &InterruptSessionsTask{} })
}

// InterruptSessionsTask is our task for interrupting sessions
type InterruptSessionsTask struct {
	ContactIDs []models.ContactID `json:"contact_ids,omitempty"`
	FlowIDs    []models.FlowID    `json:"flow_ids,omitempty"`
}

func (t *InterruptSessionsTask) Type() string {
	return TypeInterruptSessions
}

// Timeout is the maximum amount of time the task can run for
func (t *InterruptSessionsTask) Timeout() time.Duration {
	return time.Hour
}

func (t *InterruptSessionsTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *InterruptSessionsTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	db := rt.DB

	if len(t.ContactIDs) > 0 {
		if _, err := models.InterruptSessionsForContacts(ctx, db, t.ContactIDs); err != nil {
			return err
		}
	}
	if len(t.FlowIDs) > 0 {
		if err := models.InterruptSessionsForFlows(ctx, db, t.FlowIDs); err != nil {
			return err
		}
	}

	return nil
}
