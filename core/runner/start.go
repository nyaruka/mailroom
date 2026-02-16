package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// TriggerBuilder defines the interface for building a trigger for the passed in contact
type TriggerBuilder func() flows.Trigger

// StartWithLock starts the given contacts in flow sessions after obtaining locks for them.
func StartWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID, triggerBuilder TriggerBuilder, mode models.StartMode, startID models.StartID) ([]*Scene, []models.ContactID, error) {
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
