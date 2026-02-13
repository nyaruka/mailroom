package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeSendBroadcastBatch = "send_broadcast_batch"

func init() {
	RegisterType(TypeSendBroadcastBatch, func() Task { return &SendBroadcastBatch{} })
}

// SendBroadcastBatch is the task to send broadcast batches
type SendBroadcastBatch struct {
	*models.BroadcastBatch
}

func (t *SendBroadcastBatch) Type() string {
	return TypeSendBroadcastBatch
}

// Timeout is the maximum amount of time the task can run for
func (t *SendBroadcastBatch) Timeout() time.Duration {
	return time.Minute * 60
}

func (t *SendBroadcastBatch) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *SendBroadcastBatch) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	var bcast *models.Broadcast
	var err error

	// if this batch belongs to a persisted broadcast, fetch it
	if t.BroadcastID != models.NilBroadcastID {
		bcast, err = models.GetBroadcastByID(ctx, rt.DB, t.BroadcastID)
		if err != nil {
			return fmt.Errorf("error loading flow start for batch: %w", err)
		}
	} else {
		bcast = t.Broadcast // otherwise use broadcast from the task
	}

	// if this broadcast was interrupted, we're done
	if bcast.Status == models.BroadcastStatusInterrupted {
		return nil
	}

	// if this is our first batch, mark as started
	if t.IsFirst {
		if err := bcast.SetStarted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking broadcast as started: %w", err)
		}
	}

	// create this batch of messages
	if _, _, err := runner.BroadcastWithLock(ctx, rt, oa, bcast, t.BroadcastBatch, models.StartModeBackground); err != nil {
		return fmt.Errorf("error creating broadcast messages: %w", err)
	}

	// if this is our last batch, mark broadcast as done
	if t.IsLast {
		if err := bcast.SetCompleted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking broadcast as complete: %w", err)
		}
	}

	return nil
}
