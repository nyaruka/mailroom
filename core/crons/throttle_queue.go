package crons

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	throttleOutboxThreshold = 10_000
)

func init() {
	Register("throttle_queue", &ThrottleQueueCron{})
}

type ThrottleQueueCron struct {
}

func (c *ThrottleQueueCron) Next(last time.Time) time.Time {
	return Next(last, time.Second*10)
}

func (c *ThrottleQueueCron) AllInstances() bool {
	return false
}

// Run throttles processing of starts based on that org's current outbox size
func (c *ThrottleQueueCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	rc := rt.VK.Get()
	defer rc.Close()

	owners, err := rt.Queues.Throttled.Owners(ctx, rc)
	if err != nil {
		return nil, fmt.Errorf("error getting task owners: %w", err)
	}

	numPaused, numResumed := 0, 0

	for _, ownerID := range owners {
		oa, err := models.GetOrgAssets(ctx, rt, models.OrgID(ownerID))
		if err != nil {
			return nil, fmt.Errorf("error org assets for org #%d: %w", ownerID, err)
		}

		if oa.Org().OutboxCount() >= throttleOutboxThreshold {
			rt.Queues.Throttled.Pause(ctx, rc, ownerID)
			numPaused++
		} else {
			rt.Queues.Throttled.Resume(ctx, rc, ownerID)
			numResumed++
		}
	}

	return map[string]any{"paused": numPaused, "resumed": numResumed}, nil
}
