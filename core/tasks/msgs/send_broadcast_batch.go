package msgs

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeSendBroadcastBatch = "send_broadcast_batch"

func init() {
	tasks.RegisterType(TypeSendBroadcastBatch, func() tasks.Task { return &SendBroadcastBatchTask{} })
}

// SendBroadcastTask is the task send broadcast batches
type SendBroadcastBatchTask struct {
	*models.BroadcastBatch
}

func (t *SendBroadcastBatchTask) Type() string {
	return TypeSendBroadcastBatch
}

// Timeout is the maximum amount of time the task can run for
func (t *SendBroadcastBatchTask) Timeout() time.Duration {
	return time.Minute * 60
}

func (t *SendBroadcastBatchTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *SendBroadcastBatchTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
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
	sends, err := bcast.CreateMessages(ctx, rt, oa, t.BroadcastBatch)
	if err != nil {
		return fmt.Errorf("error creating broadcast messages: %w", err)
	}

	msgio.QueueMessages(ctx, rt, sends)

	// if this is our last batch, mark broadcast as done
	if t.IsLast {
		if err := bcast.SetCompleted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking broadcast as complete: %w", err)
		}
	}

	return nil
}
