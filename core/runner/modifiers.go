package runner

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/models"
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
	modifiers.TypeTicketOpen:     events.TypeTicketOpened,
	modifiers.TypeTicketAssignee: events.TypeTicketAssigneeChanged,
	modifiers.TypeTicketNote:     events.TypeTicketNoteAdded,
	modifiers.TypeTicketTopic:    events.TypeTicketTopicChanged,
}

// BulkModify bulk modifies contacts by applying modifiers and processing the resultant events.
//
// Note we don't load the user object from org assets as it's possible that the user isn't part of the org, e.g. customer support.
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
