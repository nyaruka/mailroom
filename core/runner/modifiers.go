package runner

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// ApplyModifiers modifies contacts by applying modifiers and processing the resultant events.
//
// Note we don't load the user object from org assets as it's possible that the user isn't part of the org, e.g. customer support.
func ApplyModifiers(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, modifiersByContact map[*flows.Contact][]flows.Modifier) (map[*flows.Contact][]flows.Event, error) {
	// create an environment instance with location support
	env := flows.NewAssetsEnvironment(oa.Env(), oa.SessionAssets())
	eng := goflow.Engine(rt)

	scenes := make([]*Scene, 0, len(modifiersByContact))
	eventsByContact := make(map[*flows.Contact][]flows.Event, len(modifiersByContact))

	for contact, mods := range modifiersByContact {
		scene := NewScene(nil, contact)

		// apply the modifiers to get the events
		events := make([]flows.Event, 0)
		for _, mod := range mods {
			modifiers.Apply(eng, env, oa.SessionAssets(), contact, mod, func(e flows.Event) { events = append(events, e) })
		}
		eventsByContact[contact] = events

		// TODO limit use crediting to only the first event? We might have contact_groups_changed events here from changing contact fields.

		for _, e := range events {
			if err := scene.AddEvent(ctx, rt, oa, e, userID); err != nil {
				return nil, fmt.Errorf("error adding events for contact %s: %w", contact.UUID(), err)
			}
		}

		scenes = append(scenes, scene)
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, fmt.Errorf("error committing scenes from modifiers: %w", err)
	}

	return eventsByContact, nil
}
