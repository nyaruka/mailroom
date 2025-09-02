package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// UpdateContactGroups is our hook for all group changes
var UpdateContactGroups runner.PreCommitHook = &updateContactGroups{}

type updateContactGroups struct{}

func (h *updateContactGroups) Order() int { return 10 }

func (h *updateContactGroups) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// build up our list of all adds and removes
	adds := make([]*models.GroupAdd, 0, len(scenes))
	removes := make([]*models.GroupRemove, 0, len(scenes))
	changed := make(map[models.ContactID]bool, len(scenes))

	// we remove from our groups at once, build up our list
	for _, args := range scenes {
		// we use these sets to track what our final add or remove should be
		seenAdds := make(map[models.GroupID]*models.GroupAdd)
		seenRemoves := make(map[models.GroupID]*models.GroupRemove)

		for _, e := range args {
			switch event := e.(type) {
			case *models.GroupAdd:
				seenAdds[event.GroupID] = event
				delete(seenRemoves, event.GroupID)
			case *models.GroupRemove:
				seenRemoves[event.GroupID] = event
				delete(seenAdds, event.GroupID)
			}
		}

		for _, add := range seenAdds {
			adds = append(adds, add)
			changed[add.ContactID] = true
		}

		for _, remove := range seenRemoves {
			removes = append(removes, remove)
			changed[remove.ContactID] = true
		}
	}

	if err := models.AddContactsToGroups(ctx, tx, adds); err != nil {
		return fmt.Errorf("error adding contacts to groups: %w", err)
	}

	if err := models.RemoveContactsFromGroups(ctx, tx, removes); err != nil {
		return fmt.Errorf("error removing contacts from groups: %w", err)
	}

	return nil
}
