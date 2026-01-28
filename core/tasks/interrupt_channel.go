package tasks

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeInterruptChannel is the type of the interruption of a channel
const TypeInterruptChannel = "interrupt_channel"

func init() {
	RegisterType(TypeInterruptChannel, func() Task { return &InterruptChannel{} })
}

// InterruptChannel is our task to interrupt a channel
type InterruptChannel struct {
	ChannelID models.ChannelID `json:"channel_id"`
}

func (t *InterruptChannel) Type() string {
	return TypeInterruptChannel
}

func (*InterruptChannel) Timeout() time.Duration {
	return time.Hour
}

func (t *InterruptChannel) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform implements tasks.Task
func (t *InterruptChannel) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	vc := rt.VK.Get()
	defer vc.Close()

	// load channel from db instead of assets because it may already be released
	channel, err := models.GetChannelByID(ctx, rt.DB.DB, t.ChannelID)
	if err != nil {
		return fmt.Errorf("error getting channel: %w", err)
	}

	// interrupt any IVR sessions currently using this channel
	if err := t.interruptIVRSessions(ctx, rt, oa); err != nil {
		return fmt.Errorf("error interrupting sessions: %w", err)
	}

	if err = msgio.ClearCourierQueues(vc, channel); err != nil {
		return fmt.Errorf("error clearing courier queues: %w", err)
	}

	err = models.FailChannelMessages(ctx, rt.DB.DB, oa.OrgID(), t.ChannelID, models.MsgFailedChannelRemoved)
	if err != nil {
		return fmt.Errorf("error failing channel messages: %w", err)
	}

	return nil
}

func (t *InterruptChannel) interruptIVRSessions(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	// fail any calls that are pending, queued or errored
	if _, err := rt.DB.ExecContext(ctx, `UPDATE ivr_call SET status = 'F', modified_on = NOW() WHERE channel_id = $1 AND status IN ('P', 'Q', 'E')`, t.ChannelID); err != nil {
		return fmt.Errorf("error failing queued calls on channel %d: %w", t.ChannelID, err)
	}

	// get the ongoing sessions from calls on this channel
	sessionRefs, err := models.GetWaitingSessionsForChannel(ctx, rt.DB, t.ChannelID)
	if err != nil {
		return fmt.Errorf("error selecting sessions from calls on channel %d: %w", t.ChannelID, err)
	}

	// and queue up batch tasks to interrupt them
	for batch := range slices.Chunk(sessionRefs, interruptSessionBatchSize) {
		task := &InterruptSessionBatch{Sessions: batch, Status: flows.SessionStatusInterrupted}

		if err := Queue(ctx, rt, rt.Queues.Batch, oa.OrgID(), task, false); err != nil {
			return fmt.Errorf("error queueing interrupt session batch task for channel #%d: %w", t.ChannelID, err)
		}
	}

	return nil
}
