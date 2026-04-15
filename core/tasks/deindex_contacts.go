package tasks

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// TypeDeindexContacts is the type of the deindex contacts task
const TypeDeindexContacts = "deindex_contacts"

func init() {
	RegisterType(TypeDeindexContacts, func() Task { return &DeindexContacts{} })
}

// DeindexContacts is our task for deindexing contacts. It will remove the contacts and their messages from the
// search index when the task runs.
type DeindexContacts struct {
	ContactUUIDs []flows.ContactUUID `json:"contact_uuids" validate:"required"`
}

func (t *DeindexContacts) Type() string {
	return TypeDeindexContacts
}

// Timeout is the maximum amount of time the task can run for
func (t *DeindexContacts) Timeout() time.Duration {
	return 10 * time.Minute
}

func (t *DeindexContacts) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *DeindexContacts) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	slog.Info("starting deindex contacts task", "org_id", oa.OrgID(), "contacts", len(t.ContactUUIDs))

	result, err := runner.DeIndexContacts(ctx, rt, oa.OrgID(), t.ContactUUIDs)
	if err != nil {
		return err
	}

	slog.Info("completed deindex contacts task", "org_id", oa.OrgID(), "result", result)
	return nil
}
