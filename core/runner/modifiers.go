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

// ApplyModifiers modifies contacts by applying modifiers and handling the resultant events.
//
// Note we don't load the user object from org assets as it's possible that the user isn't part of the org, e.g. customer support.
func ApplyModifiers(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, modifiersByContact map[*flows.Contact][]flows.Modifier) (map[*flows.Contact][]flows.Event, error) {
	// create an environment instance with location support
	env := flows.NewAssetsEnvironment(oa.Env(), oa.SessionAssets())

	eng := goflow.Engine(rt)

	eventsByContact := make(map[*flows.Contact][]flows.Event, len(modifiersByContact))

	// apply the modifiers to get the events for each contact
	for contact, mods := range modifiersByContact {
		events := make([]flows.Event, 0)
		for _, mod := range mods {
			modifiers.Apply(eng, env, oa.SessionAssets(), contact, mod, func(e flows.Event) { events = append(events, e) })
		}
		eventsByContact[contact] = events
	}

	if err := ApplyEvents(ctx, rt, oa, userID, eventsByContact, nil); err != nil {
		return nil, fmt.Errorf("error commiting events: %w", err)
	}

	return eventsByContact, nil
}
