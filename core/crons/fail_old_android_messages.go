package crons

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// how many messages we fail per query, so that a large backlog doesn't become one long transaction
const failOldAndroidMessagesBatchSize = 1000

func init() {
	Register("fail_old_android_messages", &FailOldAndroidMessagesCron{})
}

type FailOldAndroidMessagesCron struct{}

// Next runs this hourly rather than daily even though the messages it fails are a week old. Cron schedules here are
// intervals measured from when the instance started rather than wall clock times, so a daily interval would only
// ever fire on instances that stay up for a day, and messages could go unfailed for as long as we keep deploying.
func (c *FailOldAndroidMessagesCron) Next(last time.Time) time.Time {
	return Next(last, time.Hour)
}

func (c *FailOldAndroidMessagesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	// give up at the same age that sync_android_channels stops nudging the channel's relayer - failing sooner
	// would permanently lose messages whose relayer we're still trying to bring back
	olderThan := dates.Now().Add(-models.AndroidGiveUpAge)
	numFailed := 0

	for {
		tags, err := models.FailOldAndroidMessages(ctx, rt.DB, olderThan, failOldAndroidMessagesBatchSize)
		if err != nil {
			return nil, fmt.Errorf("error failing old android messages: %w", err)
		}
		if len(tags) == 0 {
			break
		}

		// record each change in the contact's history so that clients rendering the message see it as failed
		for _, tag := range tags {
			if _, err := rt.Dynamo.History.Queue(tag); err != nil {
				return nil, fmt.Errorf("error queuing status tag to writer: %w", err)
			}
		}

		numFailed += len(tags)
	}

	return map[string]any{"failed": numFailed}, nil
}
