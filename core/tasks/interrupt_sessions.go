package tasks

import (
	"context"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeInterruptSessions is the type of the interrupt session task
const TypeInterruptSessions = "interrupt_sessions"

func init() {
	RegisterType(TypeInterruptSessions, func() Task { return &InterruptSessions{} })
}

// InterruptSessions is our task for interrupting sessions
type InterruptSessions struct {
	ContactIDs []models.ContactID `json:"contact_ids,omitempty"`
	FlowIDs    []models.FlowID    `json:"flow_ids,omitempty"` // deprecated
}

func (t *InterruptSessions) Type() string {
	return TypeInterruptSessions
}

// Timeout is the maximum amount of time the task can run for
func (t *InterruptSessions) Timeout() time.Duration {
	return time.Hour
}

func (t *InterruptSessions) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *InterruptSessions) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	if len(t.ContactIDs) > 0 {
		if _, _, err := runner.InterruptWithLock(ctx, rt, oa, t.ContactIDs, nil, flows.SessionStatusInterrupted); err != nil {
			return err
		}
	}
	if len(t.FlowIDs) > 0 {
		if err := models.InterruptSessionsForFlows(ctx, rt.DB, t.FlowIDs); err != nil {
			return err
		}
	}

	return nil
}
