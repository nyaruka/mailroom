package crons

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"golang.org/x/exp/maps"
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
	vc := rt.VK.Get()
	defer vc.Close()

	ownersQueued, err := rt.Queues.Throttled.Queued(ctx, vc)
	if err != nil {
		return nil, fmt.Errorf("error getting queued task owners: %w", err)
	}
	ownersPaused, err := rt.Queues.Throttled.Paused(ctx, vc)
	if err != nil {
		return nil, fmt.Errorf("error getting paused task owners: %w", err)
	}

	// combine into a single set of org IDs
	orgIDs := make(map[models.OrgID]bool, len(ownersQueued)+len(ownersPaused))
	for _, ownerID := range ownersQueued {
		orgIDs[models.OrgID(ownerID)] = true
	}
	for _, ownerID := range ownersPaused {
		orgIDs[models.OrgID(ownerID)] = true
	}

	// and lookup all outbox counts
	outboxCounts, err := models.GetOutboxCounts(ctx, rt.DB.DB, maps.Keys(orgIDs))
	if err != nil {
		return nil, fmt.Errorf("error getting outbox counts: %w", err)
	}

	numPaused, numResumed := 0, 0

	for _, ownerID := range ownersQueued {
		if outboxCounts[models.OrgID(ownerID)] >= throttleOutboxThreshold && !slices.Contains(ownersPaused, ownerID) {
			if err := rt.Queues.Throttled.Pause(ctx, vc, int(ownerID)); err != nil {
				return nil, fmt.Errorf("error pausing org %d: %w", ownerID, err)
			}
			numPaused++
		}
	}

	for _, ownerID := range ownersPaused {
		if outboxCounts[models.OrgID(ownerID)] < throttleOutboxThreshold {
			if err := rt.Queues.Throttled.Resume(ctx, vc, int(ownerID)); err != nil {
				return nil, fmt.Errorf("error resuming org %d: %w", ownerID, err)
			}
			numResumed++
		}
	}

	return map[string]any{"paused": numPaused, "resumed": numResumed}, nil
}
