package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// InterruptWithLock interrupts sessions for the given contacts. If sessions is provided then only those sessions will
// be interrupted (if they are still the current waiting session for the contact). Otherwise the waiting session for
// each contact is interrupted. Returns any generated events and any contacts we were unable to lock.
func InterruptWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID, sessions map[models.ContactID]flows.SessionUUID, status flows.SessionStatus) (map[*flows.Contact][]flows.Event, []models.ContactID, error) {
	scenes, skipped, unlock, err := LockAndLoad(ctx, rt, oa, contactIDs, nil, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}

	defer unlock() // contacts are unlocked whatever happens

	if err := addInterruptEvents(ctx, rt, oa, scenes, sessions, status); err != nil {
		return nil, nil, fmt.Errorf("error interrupting existing sessions: %w", err)
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, nil, fmt.Errorf("error committing interruption scenes: %w", err)
	}

	evts := make(map[*flows.Contact][]flows.Event, len(scenes))
	for _, s := range scenes {
		evts[s.Contact] = s.Events()
	}

	return evts, skipped, nil
}

// adds contact interruption to the given scenes
func addInterruptEvents(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes []*Scene, sessions map[models.ContactID]flows.SessionUUID, status flows.SessionStatus) error {
	sessionUUIDs := make([]flows.SessionUUID, 0, len(scenes))
	byScene := make(map[*Scene]flows.SessionUUID, len(scenes))
	for _, s := range scenes {
		waitingSession := s.DBContact.CurrentSessionUUID()

		// if we have a waiting session and it matches the specified session (or no session specified), add it
		if waitingSession != "" && (sessions == nil || sessions[s.DBContact.ID()] == waitingSession || sessions[s.DBContact.ID()] == "") {
			sessionUUIDs = append(sessionUUIDs, waitingSession)
			byScene[s] = waitingSession
		}
	}
	if len(sessionUUIDs) == 0 {
		return nil // nothing to do
	}

	runRefs, err := models.GetActiveAndWaitingRuns(ctx, rt, sessionUUIDs)
	if err != nil {
		return fmt.Errorf("error getting active runs for waiting sessions: %w", err)
	}

	for _, s := range scenes {
		if sessionUUID := byScene[s]; sessionUUID != "" {
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
