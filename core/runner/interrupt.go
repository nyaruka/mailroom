package runner

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// InterruptWithLock interrupts the waiting sessions for the given contacts
func InterruptWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID, status flows.SessionStatus) (map[*flows.Contact][]flows.Event, []models.ContactID, error) {
	scenes, skipped, unlock, err := LockAndLoad(ctx, rt, oa, contactIDs, nil, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}

	defer unlock() // contacts are unlocked whatever happens

	if err := addInterruptEvents(ctx, rt, oa, scenes, status); err != nil {
		return nil, nil, fmt.Errorf("error interrupting existing sessions: %w", err)
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, nil, fmt.Errorf("error committing interruption scenes: %w", err)
	}

	eventsByContact := make(map[*flows.Contact][]flows.Event, len(scenes))
	for _, s := range scenes {
		eventsByContact[s.Contact] = s.History()
	}

	return eventsByContact, skipped, nil
}

// adds contact interruption to the given scenes
func addInterruptEvents(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes []*Scene, status flows.SessionStatus) error {
	sessions := make(map[flows.SessionUUID]*Scene, len(scenes))
	for _, s := range scenes {
		if s.DBContact.CurrentSessionUUID() != "" {
			sessions[s.DBContact.CurrentSessionUUID()] = s
		}
	}
	if len(sessions) == 0 {
		return nil // nothing to do
	}

	runRefs, err := models.GetActiveAndWaitingRuns(ctx, rt, slices.Collect(maps.Keys(sessions)))
	if err != nil {
		return fmt.Errorf("error getting active runs for waiting sessions: %w", err)
	}

	for _, s := range scenes {
		if s.DBContact.CurrentSessionUUID() != "" {
			if err := s.AddEvent(ctx, rt, oa, newContactInterruptedEvent(status), models.NilUserID, ""); err != nil {
				return fmt.Errorf("error adding contact interrupted event: %w", err)
			}

			for _, run := range runRefs[s.DBContact.CurrentSessionUUID()] {
				if err := s.AddEvent(ctx, rt, oa, events.NewRunEnded(run.UUID, run.Flow, flows.RunStatus(status)), models.NilUserID, ""); err != nil {
					return fmt.Errorf("error adding run ended event: %w", err)
				}
			}
		}
	}

	return nil
}
