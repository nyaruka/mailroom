package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// UpdateContactStatus is our hook for contact status changes
var UpdateContactStatus runner.PreCommitHook = &updateContactStatus{}

type updateContactStatus struct{}

func (h *updateContactStatus) Order() int { return 10 }

func (h *updateContactStatus) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	statusChanges := make([]*models.ContactStatusChange, 0, len(scenes))
	for scene, es := range scenes {
		event := es[len(es)-1].(*events.ContactStatusChanged)
		statusChanges = append(statusChanges, &models.ContactStatusChange{ContactID: scene.ContactID(), Status: event.Status})
	}

	if err := models.UpdateContactStatus(ctx, tx, statusChanges); err != nil {
		return fmt.Errorf("error updating contact statuses: %w", err)
	}
	return nil
}
