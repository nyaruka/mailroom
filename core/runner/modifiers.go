package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// ModifyWithLock bulk modifies contacts by locking and loading them, applying modifiers and processing the resultant events.
func ModifyWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, contactIDs []models.ContactID, mods map[models.ContactID][]flows.Modifier, includeTickets map[models.ContactID][]*models.Ticket, via models.Via) (map[*flows.Contact][]flows.Event, []models.ContactID, error) {
	scenes, skipped, unlock, err := LockAndLoad(ctx, rt, oa, contactIDs, includeTickets, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}

	defer unlock() // contacts are unlocked whatever happens

	evts := make(map[*flows.Contact][]flows.Event, len(mods))

	for _, scene := range scenes {
		for _, mod := range mods[scene.ContactID()] {
			_, err := scene.ApplyModifier(ctx, rt, oa, mod, userID, via)
			if err != nil {
				return nil, nil, fmt.Errorf("error applying modifier %T to contact %s: %w", mod, scene.ContactUUID(), err)
			}
		}

		evts[scene.Contact] = scene.Events()
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, nil, fmt.Errorf("error committing scenes from modifiers: %w", err)
	}

	return evts, skipped, nil
}

// ModifyWithoutLock bulk modifies contacts without locking.. used during contact creation and imports
func ModifyWithoutLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, mcs []*models.Contact, contacts []*flows.Contact, modifiers map[flows.ContactUUID][]flows.Modifier, via models.Via) (map[*flows.Contact][]flows.Event, error) {
	scenes := make([]*Scene, 0, len(mcs))
	evts := make(map[*flows.Contact][]flows.Event, len(mcs))

	for i, mc := range mcs {
		contact := contacts[i]
		scene := NewScene(mc, contact)
		evts[contact] = make([]flows.Event, 0)

		for _, mod := range modifiers[mc.UUID()] {
			_, err := scene.ApplyModifier(ctx, rt, oa, mod, userID, via)
			if err != nil {
				return nil, fmt.Errorf("error applying modifier %T to contact %s: %w", mod, mc.UUID(), err)
			}
		}

		scenes = append(scenes, scene)
		evts[scene.Contact] = scene.Events()
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, fmt.Errorf("error committing scenes from modifiers: %w", err)
	}

	return evts, nil
}
