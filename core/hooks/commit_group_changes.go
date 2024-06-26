package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"

	"github.com/jmoiron/sqlx"
)

// CommitGroupChangesHook is our hook for all group changes
var CommitGroupChangesHook models.EventCommitHook = &commitGroupChangesHook{}

type commitGroupChangesHook struct{}

// Apply squashes and adds or removes all our contact groups
func (h *commitGroupChangesHook) Apply(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*models.Scene][]any) error {
	// build up our list of all adds and removes
	adds := make([]*models.GroupAdd, 0, len(scenes))
	removes := make([]*models.GroupRemove, 0, len(scenes))
	changed := make(map[models.ContactID]bool, len(scenes))

	// we remove from our groups at once, build up our list
	for _, events := range scenes {
		// we use these sets to track what our final add or remove should be
		seenAdds := make(map[models.GroupID]*models.GroupAdd)
		seenRemoves := make(map[models.GroupID]*models.GroupRemove)

		for _, e := range events {
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

	// do our updates
	err := models.AddContactsToGroups(ctx, tx, adds)
	if err != nil {
		return fmt.Errorf("error adding contacts to groups: %w", err)
	}

	err = models.RemoveContactsFromGroups(ctx, tx, removes)
	if err != nil {
		return fmt.Errorf("error removing contacts from groups: %w", err)
	}

	return nil
}
