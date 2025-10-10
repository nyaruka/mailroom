package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// UpdateContactStatus is our hook for contact status changes
var UpdateContactStatus runner.PreCommitHook = &updateContactStatus{}

type updateContactStatus struct{}

func (h *updateContactStatus) Order() int { return 10 }

func (h *updateContactStatus) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	statusChanges := make([]*models.ContactStatusChange, 0, len(scenes))
	for scene, args := range scenes {
		event := args[len(args)-1].(*events.ContactStatusChanged)
		statusChanges = append(statusChanges, &models.ContactStatusChange{ContactID: scene.ContactID(), Status: event.Status})
	}

	if err := models.UpdateContactStatus(ctx, tx, statusChanges); err != nil {
		return fmt.Errorf("error updating contact statuses: %w", err)
	}
	return nil
}
