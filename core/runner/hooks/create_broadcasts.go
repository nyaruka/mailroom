package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// CreateBroadcasts is our hook for creating broadcasts
var CreateBroadcasts runner.PostCommitHook = &createBroadcasts{}

type createBroadcasts struct{}

func (h *createBroadcasts) Order() int { return 10 }

func (h *createBroadcasts) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// for each of our scene
	for _, args := range scenes {
		for _, e := range args {
			event := e.(*events.BroadcastCreated)

			// create a non-persistent broadcast
			bcast, err := models.NewBroadcastFromEvent(ctx, rt.DB, oa, event)
			if err != nil {
				return fmt.Errorf("error creating broadcast: %w", err)
			}

			err = tasks.Queue(ctx, rt, rt.Queues.Batch, oa.OrgID(), &tasks.SendBroadcast{Broadcast: bcast}, false)
			if err != nil {
				return fmt.Errorf("error queuing broadcast task: %w", err)
			}
		}
	}

	return nil
}
