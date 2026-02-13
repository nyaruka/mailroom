package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// BroadcastWithLock sends out a broadcast to all contacts in the provided batch
func BroadcastWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, broadcast *models.Broadcast, batch *models.BroadcastBatch, mode models.StartMode) ([]*Scene, []models.ContactID, error) {
	scenes, skipped, unlock, err := LockAndLoad(ctx, rt, oa, batch.ContactIDs, nil, time.Minute)
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

		scene.Broadcast = broadcast

		event, err := broadcast.Send(ctx, rt, oa, scene.Contact)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating broadcast message event for contact %d: %w", scene.Contact.ID(), err)
		}

		if event != nil {
			if err := scene.AddEvent(ctx, rt, oa, event, broadcast.CreatedByID, ""); err != nil {
				return nil, nil, fmt.Errorf("error adding message event to broadcast scene: %w", err)
			}
		}
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, nil, fmt.Errorf("error committing broadcast scenes: %w", err)
	}

	return scenes, skipped, nil
}
