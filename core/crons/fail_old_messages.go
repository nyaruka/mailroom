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
const failOldMessagesBatchSize = 1000

func init() {
	Register("fail_old_messages", &FailOldMessagesCron{})
}

// FailOldMessagesCron fails outgoing messages which have been waiting to be sent for so long that nothing is going
// to send them, so that they don't sit in the outbox forever - see models.MsgGiveUpAge.
type FailOldMessagesCron struct{}

// Next runs this hourly for the same reason as FailOldAndroidMessagesCron - cron schedules here are intervals
// measured from when the instance started, so a longer one might never fire.
func (c *FailOldMessagesCron) Next(last time.Time) time.Time {
	return Next(last, time.Hour)
}

func (c *FailOldMessagesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	olderThan := dates.Now().Add(-models.MsgGiveUpAge)
	numFailed := 0

	// only orgs with something in their outbox can have anything to fail
	orgIDs, err := models.GetOrgIDsWithOutbox(ctx, rt.DB)
	if err != nil {
		return nil, fmt.Errorf("error getting orgs with an outbox: %w", err)
	}

	for _, orgID := range orgIDs {
		for {
			tags, err := models.FailOldMessages(ctx, rt.DB, orgID, olderThan, failOldMessagesBatchSize)
			if err != nil {
				return nil, fmt.Errorf("error failing old messages for org %d: %w", orgID, err)
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
	}

	return map[string]any{"failed": numFailed}, nil
}
