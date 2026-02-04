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

// UpdateContactLastSeenOn is our hook for contact changes that require an update to last_seen_on
var UpdateContactLastSeenOn runner.PreCommitHook = &updateContactLastSeenOn{}

type updateContactLastSeenOn struct{}

func (h *updateContactLastSeenOn) Order() int { return 10 }

func (h *updateContactLastSeenOn) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for scene, args := range scenes {
		event := args[len(args)-1].(*events.ContactLastSeenChanged)

		// currently we don't handle incoming messages/events in batches so we don't need to worry about batching this
		if err := scene.DBContact.UpdateLastSeenOn(ctx, tx, event.LastSeenOn); err != nil {
			return fmt.Errorf("error updating last_seen_on on contact: %w", err)
		}
	}

	return nil
}
