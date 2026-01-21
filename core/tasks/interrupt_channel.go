package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/runner"
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
	contactIDs := make([]models.ContactID, 0, 10)

	// fail any calls that are pending, queued or errored
	if _, err := rt.DB.ExecContext(ctx, `UPDATE ivr_call SET status = 'F', modified_on = NOW() WHERE channel_id = $1 AND status IN ('P', 'Q', 'E')`, t.ChannelID); err != nil {
		return fmt.Errorf("error failing queued calls on channel %d: %w", t.ChannelID, err)
	}

	// find all contacts with calls in progress...
	if err := rt.DB.SelectContext(ctx, &contactIDs, `SELECT contact_id FROM ivr_call WHERE channel_id = $1 AND status = 'I' AND session_uuid IS NOT NULL`, t.ChannelID); err != nil {
		return fmt.Errorf("error selecting contacts with calls on channel %d: %w", t.ChannelID, err)
	}

	// and interrupt their sessions
	if _, _, err := runner.InterruptWithLock(ctx, rt, oa, contactIDs, flows.SessionStatusInterrupted); err != nil {
		return fmt.Errorf("error interrupting contacts with calls on channel %d: %w", t.ChannelID, err)
	}

	return nil
}
