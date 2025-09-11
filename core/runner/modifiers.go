package runner

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/clocks"
	"github.com/nyaruka/mailroom/runtime"
)

// Mapping of modifier types to the primary event type they generate which is the only event that should
// be credited to the user. For example if a user changes a field value that generates a contact_field_changed
// event (which should be credited to the user) but potentially also a contact_groups_changed event (which should not).
var modifierUserEvents = map[string]string{
	modifiers.TypeField:          events.TypeContactFieldChanged,
	modifiers.TypeGroups:         events.TypeContactGroupsChanged,
	modifiers.TypeLanguage:       events.TypeContactLanguageChanged,
	modifiers.TypeName:           events.TypeContactNameChanged,
	modifiers.TypeStatus:         events.TypeContactStatusChanged,
	modifiers.TypeTicketAssignee: events.TypeTicketAssigneeChanged,
	modifiers.TypeTicketClose:    events.TypeTicketClosed,
	modifiers.TypeTicketNote:     events.TypeTicketNoteAdded,
	modifiers.TypeTicketOpen:     events.TypeTicketOpened,
	modifiers.TypeTicketTopic:    events.TypeTicketTopicChanged,
}

// ModifyWithLock bulk modifies contacts by loading and locking them, applying modifiers and processing the resultant events.
//
// Note we don't load the user object from org assets as it's possible that the user isn't part of the org, e.g. customer support.
func ModifyWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, contactIDs []models.ContactID, modifiersByContact map[models.ContactID][]flows.Modifier, includeTickets map[models.ContactID][]*models.Ticket) (map[*flows.Contact][]flows.Event, []models.ContactID, error) {
	// we now need to grab locks for our contacts so that they are never in two starts or handles at the
	// same time we try to grab locks for up to a minute, but do it in batches where we wait for one
	// second per contact to prevent deadlocks
	eventsByContact := make(map[*flows.Contact][]flows.Event, len(modifiersByContact))
	remaining := contactIDs

	start := time.Now()

	for len(remaining) > 0 && time.Since(start) < time.Minute {
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
	locked := slices.Sorted(maps.Keys(locks))

	// whatever happens, we need to unlock the contacts
	defer clocks.Unlock(ctx, rt, oa, locks)

	eventsByContact := make(map[*flows.Contact][]flows.Event, len(ids))

	// create scenes for the locked contacts
	scenes, err := CreateScenes(ctx, rt, oa, locked, includeTickets)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating scenes for modifiers: %w", err)
	}

	for _, scene := range scenes {
		eventsByContact[scene.Contact] = make([]flows.Event, 0) // TODO only needed to avoid nulls until jsonv2

		for _, mod := range modifiersByContact[scene.ContactID()] {
			evts, err := scene.ApplyModifier(ctx, rt, oa, mod, userID)
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

// BulkModify bulk modifies contacts without locking.. used during contact creation
func BulkModify(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, modifiersByContact map[*flows.Contact][]flows.Modifier) (map[*flows.Contact][]flows.Event, error) {
	scenes := make([]*Scene, 0, len(modifiersByContact))
	eventsByContact := make(map[*flows.Contact][]flows.Event, len(modifiersByContact))

	// until go has an easier way to iterate a map in a stable order, we need this to make tests deterministic
	contactsByID := slices.SortedFunc(maps.Keys(modifiersByContact), func(a, b *flows.Contact) int { return cmp.Compare(a.ID(), b.ID()) })

	for _, contact := range contactsByID {
		scene := NewScene(nil, contact)
		eventsByContact[contact] = make([]flows.Event, 0)

		for _, mod := range modifiersByContact[contact] {
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
