package runner

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/clocks"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	// how long we will keep trying to lock contacts for modification
	modifyLockWait = 10 * time.Second
)

// ModifyWithLock bulk modifies contacts by loading and locking them, applying modifiers and processing the resultant events.
//
// Note we don't load the user object from org assets as it's possible that the user isn't part of the org, e.g. customer support.
func ModifyWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, contactIDs []models.ContactID, modifiersByContact map[models.ContactID][]flows.Modifier, includeTickets map[models.ContactID][]*models.Ticket) (map[*flows.Contact][]flows.Event, []models.ContactID, error) {
	eventsByContact := make(map[*flows.Contact][]flows.Event, len(modifiersByContact))
	remaining := contactIDs
	start := time.Now()

	for len(remaining) > 0 && time.Since(start) < modifyLockWait {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}

		es, skipped, err := tryToModifyWithLock(ctx, rt, oa, userID, remaining, modifiersByContact, includeTickets)
		if err != nil {
			return nil, nil, err
		}

		maps.Copy(eventsByContact, es)
		remaining = skipped // skipped are now our remaining
	}

	return eventsByContact, remaining, nil
}

func tryToModifyWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, ids []models.ContactID, modifiersByContact map[models.ContactID][]flows.Modifier, includeTickets map[models.ContactID][]*models.Ticket) (map[*flows.Contact][]flows.Event, []models.ContactID, error) {
	// try to get locks for these contacts, waiting for up to a second for each contact
	locks, skipped, err := clocks.TryToLock(ctx, rt, oa, ids, time.Second)
	if err != nil {
		return nil, nil, err
	}
	locked := slices.Collect(maps.Keys(locks))

	// whatever happens, we need to unlock the contacts
	defer clocks.Unlock(ctx, rt, oa, locks)

	eventsByContact := make(map[*flows.Contact][]flows.Event, len(ids))

	// create scenes for the locked contacts
	scenes, err := CreateScenes(ctx, rt, oa, locked, includeTickets)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating scenes for modifiers: %w", err)
	}

	// for test determinism
	slices.SortFunc(scenes, func(a, b *Scene) int { return cmp.Compare(a.Contact.ID(), b.Contact.ID()) })

	for _, scene := range scenes {
		eventsByContact[scene.Contact] = make([]flows.Event, 0) // TODO only needed to avoid nulls until jsonv2

		for _, mod := range modifiersByContact[scene.ContactID()] {
			evts, err := scene.ApplyModifier(ctx, rt, oa, mod, userID, "")
			if err != nil {
				return nil, nil, fmt.Errorf("error applying modifier: %w", err)
			}

			eventsByContact[scene.Contact] = append(eventsByContact[scene.Contact], evts...)
		}
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, nil, fmt.Errorf("error committing scenes from modifiers: %w", err)
	}

	return eventsByContact, skipped, nil
}

// BulkModify bulk modifies contacts without locking.. used during contact creation and imports
func BulkModify(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, mcs []*models.Contact, contacts []*flows.Contact, modifiers map[flows.ContactUUID][]flows.Modifier, via Via) (map[*flows.Contact][]flows.Event, error) {
	scenes := make([]*Scene, 0, len(mcs))
	eventsByContact := make(map[*flows.Contact][]flows.Event, len(mcs))

	for i, mc := range mcs {
		contact := contacts[i]
		scene := NewScene(mc, contact)
		eventsByContact[contact] = make([]flows.Event, 0)

		for _, mod := range modifiers[mc.UUID()] {
			evts, err := scene.ApplyModifier(ctx, rt, oa, mod, userID, via)
			if err != nil {
				return nil, fmt.Errorf("error applying modifier %T to contact %s: %w", mod, mc.UUID(), err)
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
