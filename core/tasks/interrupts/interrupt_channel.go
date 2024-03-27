package interrupts

import (
	"context"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/pkg/errors"
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
	rc := rt.RP.Get()
	defer rc.Close()

	// load channel from db instead of assets because it may already be released
	channels, err := models.GetChannelsByID(ctx, db.DB, []models.ChannelID{t.ChannelID})
	if err != nil {
		return errors.Wrapf(err, "error getting channels")
	}

	channel := channels[0]

	if err := models.InterruptSessionsForChannel(ctx, db, t.ChannelID); err != nil {
		return errors.Wrapf(err, "error interrupting sessions")
	}

	err = msgio.ClearCourierQueues(rc, channel)
	if err != nil {
		return errors.Wrapf(err, "error clearing courier queues")
	}

	err = models.FailChannelMessages(ctx, rt.DB.DB, oa.OrgID(), t.ChannelID, models.MsgFailedChannelRemoved)
	if err != nil {
		return errors.Wrapf(err, "error failing channel messages")
	}

	return nil

}

// Timeout is the maximum amount of time the task can run for
func (*InterruptChannelTask) Timeout() time.Duration {
	return time.Hour
}
