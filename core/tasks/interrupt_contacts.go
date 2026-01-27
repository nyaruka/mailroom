package tasks

import (
	"context"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeInterruptContacts is the type of the interrupt contacts task
const TypeInterruptContacts = "interrupt_contacts"

func init() {
	RegisterType(TypeInterruptContacts, func() Task { return &InterruptContacts{} })
}

// InterruptContacts is our task for interrupting contacts. It will interrupt whatever is the current session for the
// contact when the task runs.
type InterruptContacts struct {
	ContactIDs []models.ContactID `json:"contact_ids" validate:"required"`
}

func (t *InterruptContacts) Type() string {
	return TypeInterruptContacts
}

// Timeout is the maximum amount of time the task can run for
func (t *InterruptContacts) Timeout() time.Duration {
	return 10 * time.Minute
}

func (t *InterruptContacts) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *InterruptContacts) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	if _, _, err := runner.InterruptWithLock(ctx, rt, oa, t.ContactIDs, nil, flows.SessionStatusInterrupted); err != nil {
		return err
	}

	return nil
}
