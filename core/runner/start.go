package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// TriggerBuilder defines the interface for building a trigger for the passed in contact
type TriggerBuilder func() flows.Trigger

// EventBuilder defines the interface for building an event for the passed in contact
type EventBuilder func() events.Event

// StartWithLock starts the given contacts in flow sessions after obtaining locks for them. If skipEvent is provided,
// contacts skipped because of the start mode get that event added to their history.
func StartWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID, triggerBuilder TriggerBuilder, mode models.StartMode, startID models.StartID, skipEvent EventBuilder) ([]*Scene, []models.ContactID, error) {
	scenes, skipped, unlock, err := LockAndLoad(ctx, rt, oa, contactIDs, nil, time.Minute)
	if err != nil {
		return nil, nil, err
	}

	defer unlock() // contacts are unlocked whatever happens

	if mode == models.StartModeInterrupt {
		if err := addInterruptEvents(ctx, rt, oa, scenes, nil, flows.SessionStatusInterrupted); err != nil {
			return nil, nil, fmt.Errorf("error interrupting existing sessions: %w", err)
		}
	}

	for _, scene := range scenes {
		if mode == models.StartModeSkip && scene.DBContact.CurrentSessionUUID() != "" {
			if err := addSkipEvent(ctx, rt, oa, scene, skipEvent); err != nil {
				return nil, nil, err
			}
			continue
		}

		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("error starting session: %w", ctx.Err())
		}

		scene.StartID = startID

		if err := scene.StartSession(ctx, rt, oa, triggerBuilder(), false); err != nil {
			return nil, nil, fmt.Errorf("error starting session for contact %s: %w", scene.ContactUUID(), err)
		}
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, nil, fmt.Errorf("error committing scenes: %w", err)
	}

	return scenes, skipped, nil
}

func addSkipEvent(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *Scene, skipEvent EventBuilder) error {
	if skipEvent == nil {
		return nil
	}
	if err := scene.AddEvent(ctx, rt, oa, skipEvent(), models.NilUserID, ""); err != nil {
		return fmt.Errorf("error adding skip event for contact %s: %w", scene.ContactUUID(), err)
	}
	return nil
}

// LogWithLock adds an event to the history of each of the given contacts after obtaining locks for them.
func LogWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID, eventBuilder EventBuilder) ([]models.ContactID, error) {
	scenes, skipped, unlock, err := LockAndLoad(ctx, rt, oa, contactIDs, nil, time.Minute)
	if err != nil {
		return nil, err
	}

	defer unlock() // contacts are unlocked whatever happens

	for _, scene := range scenes {
		if err := scene.AddEvent(ctx, rt, oa, eventBuilder(), models.NilUserID, ""); err != nil {
			return nil, fmt.Errorf("error adding event for contact %s: %w", scene.ContactUUID(), err)
		}
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, fmt.Errorf("error committing scenes: %w", err)
	}

	return skipped, nil
}
