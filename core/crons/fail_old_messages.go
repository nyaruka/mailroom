package crons

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/core/events"
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

	// an org we can't do is left for the next run rather than stopping us getting to the rest
	var orgErr error
	numErrored := 0

	// what we got done is reported even when we return an error
	results := func() map[string]any {
		return map[string]any{"failed": numFailed, "errored": numErrored}
	}

	for _, orgID := range orgIDs {
		n, err := c.failForOrg(ctx, rt, orgID, olderThan)
		numFailed += n

		if err != nil {
			// unless we're out of time, in which case nothing else is going to work either
			if ctx.Err() != nil {
				return results(), fmt.Errorf("error failing old messages for org %d: %w", orgID, err)
			}

			// only the first is returned, to be logged by our caller, so log the others here
			numErrored++
			if orgErr == nil {
				orgErr = fmt.Errorf("error failing old messages for org %d: %w", orgID, err)
			} else {
				slog.Error("error failing old messages", "org_id", orgID, "error", err)
			}
		}
	}

	return results(), orgErr
}

// fails the old messages of a single org, a batch at a time, returning how many were failed
func (c *FailOldMessagesCron) failForOrg(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, olderThan time.Time) (int, error) {
	numFailed := 0
	before := events.EventUUID("")

	for {
		tags, err := models.FailOldMessages(ctx, rt.DB, orgID, olderThan, before, failOldMessagesBatchSize)
		if err != nil {
			return numFailed, err
		}

		// record each change in the contact's history so that clients rendering the message see it as failed
		for _, tag := range tags {
			if _, err := rt.Dynamo.History.Queue(tag); err != nil {
				return numFailed, fmt.Errorf("error queuing status tag to writer: %w", err)
			}

			// the next batch carries on from the oldest message of this one
			if before == "" || tag.EventUUID < before {
				before = tag.EventUUID
			}
		}

		numFailed += len(tags)

		if len(tags) < failOldMessagesBatchSize {
			return numFailed, nil
		}
	}
}
