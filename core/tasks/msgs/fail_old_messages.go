package msgs

import (
	"context"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	tasks.RegisterCron("fail_old_messages", &FailOldMessagesCron{})
}

type FailOldMessagesCron struct{}

func (c *FailOldMessagesCron) Next(last time.Time) time.Time {
	return tasks.CronNext(last, time.Hour*24)
}

func (c *FailOldMessagesCron) AllInstances() bool {
	return false
}

func (c *FailOldMessagesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	db := rt.DB
	rc := rt.RP.Get()
	defer rc.Close()

	msgs, err := models.FailOldMessages(ctx, db)
	if err != nil {
		return nil, err
	}

	seen := make(map[models.ChannelID]bool)
	channelIds := make([]models.ChannelID, len(msgs))
	for _, msg := range msgs {
		key := msg.ChannelID()
		if !seen[key] {
			seen[key] = true
			channelIds = append(channelIds, msg.ChannelID())
		}
	}

	channels, err := models.GetChannelsByID(ctx, db.DB, channelIds)
	if err != nil {
		return nil, err
	}
	for _, ch := range channels {
		msgio.ClearFailedOldMessagesCourierQueues(rc, ch)
	}

	return map[string]any{"failed": len(msgs)}, nil
}
