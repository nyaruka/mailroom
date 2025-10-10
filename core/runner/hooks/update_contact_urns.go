package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// UpdateContactURNs is our hook for when a URN is added to a contact
var UpdateContactURNs runner.PreCommitHook = &updateContactURNs{}

type updateContactURNs struct{}

func (h *updateContactURNs) Order() int { return 10 }

func (h *updateContactURNs) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our urn changes, we only care about the last change for each scene
	changes := make([]*models.ContactURNsChanged, 0, len(scenes))
	for _, args := range scenes {
		urnChange := args[len(args)-1].(*models.ContactURNsChanged)
		changes = append(changes, urnChange)
	}

	if err := models.UpdateContactURNs(ctx, tx, oa, changes); err != nil {
		return fmt.Errorf("error updating contact urns: %w", err)
	}

	return nil
}
