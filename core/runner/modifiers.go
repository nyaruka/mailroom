package runner

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// BulkModify bulk modifies contacts by applying modifiers and processing the resultant events.
//
// Note we don't load the user object from org assets as it's possible that the user isn't part of the org, e.g. customer support.
func BulkModify(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, modifiersByContact map[*flows.Contact][]flows.Modifier) (map[*flows.Contact][]flows.Event, error) {
	scenes := make([]*Scene, 0, len(modifiersByContact))
	eventsByContact := make(map[*flows.Contact][]flows.Event, len(modifiersByContact))

	for contact, mods := range modifiersByContact {
		scene := NewScene(nil, contact)
		eventsByContact[contact] = make([]flows.Event, 0)

		for _, mod := range mods {
			evts, err := scene.ApplyModifier(ctx, rt, oa, mod, userID)
			if err != nil {
				return nil, fmt.Errorf("error applying modifier %T to contact %s: %w", mod, contact.UUID(), err)
			}

			eventsByContact[contact] = append(eventsByContact[contact], evts...)
		}

		scenes = append(scenes, scene)
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, fmt.Errorf("error committing scenes from modifiers: %w", err)
	}

	return eventsByContact, nil
}
