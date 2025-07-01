package interrupts

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeInterruptChannel is the type of the interruption of a channel
const TypeInterruptChannel = "interrupt_channel"

func init() {
	tasks.RegisterType(TypeInterruptChannel, func() tasks.Task { return &InterruptChannelTask{} })
}

// InterruptChannelTask is our task to interrupt a channel
type InterruptChannelTask struct {
	ChannelID models.ChannelID `json:"channel_id"`
}

func (t *InterruptChannelTask) Type() string {
	return TypeInterruptChannel
}

func (t *InterruptChannelTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform implements tasks.Task
func (t *InterruptChannelTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	db := rt.DB
	rc := rt.VK.Get()
	defer rc.Close()

	// load channel from db instead of assets because it may already be released
	channel, err := models.GetChannelByID(ctx, db.DB, t.ChannelID)
	if err != nil {
		return fmt.Errorf("error getting channel: %w", err)
	}

	if err := models.InterruptSessionsForChannel(ctx, db, t.ChannelID); err != nil {
		return fmt.Errorf("error interrupting sessions: %w", err)
	}

	if err = msgio.ClearCourierQueues(rc, channel); err != nil {
		return fmt.Errorf("error clearing courier queues: %w", err)
	}

	err = models.FailChannelMessages(ctx, rt.DB.DB, oa.OrgID(), t.ChannelID, models.MsgFailedChannelRemoved)
	if err != nil {
		return fmt.Errorf("error failing channel messages: %w", err)
	}

	return nil

}

// Timeout is the maximum amount of time the task can run for
func (*InterruptChannelTask) Timeout() time.Duration {
	return time.Hour
}
