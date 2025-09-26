package runner

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// Broadcast sends out a broadcast to all contacts in the provided batch
func Broadcast(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, broadcast *models.Broadcast, batch *models.BroadcastBatch) error {
	scenes, err := CreateScenes(ctx, rt, oa, batch.ContactIDs, nil)
	if err != nil {
		return fmt.Errorf("error creating broadcast scenes: %w", err)
	}

	for _, scene := range scenes {
		scene.Broadcast = broadcast

		if event := broadcast.Send(rt, oa, scene.Contact); event != nil {
			if err := scene.AddEvent(ctx, rt, oa, event, broadcast.CreatedByID); err != nil {
				return fmt.Errorf("error adding message event to broadcast scene: %w", err)
			}
		}
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return fmt.Errorf("error committing broadcast scenes: %w", err)
	}

	return nil
}
