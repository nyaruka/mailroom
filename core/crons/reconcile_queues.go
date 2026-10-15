package crons

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/utils/queues"
)

func init() {
	Register("reconcile_queues", &ReconcileQueuesCron{})
}

// ReconcileQueuesCron corrects any drift in the task queues' per-org active counts, e.g. slots held without leases
type ReconcileQueuesCron struct{}

func (c *ReconcileQueuesCron) Next(last time.Time) time.Time {
	return Next(last, time.Minute*5)
}

func (c *ReconcileQueuesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	vc := rt.VK.Get()
	defer vc.Close()

	res := make(map[string]any, 6)
	var errs []error

	for _, q := range []queues.Fair{rt.Queues.Realtime, rt.Queues.Batch, rt.Queues.Throttled} {
		released, claimed, err := q.Reconcile(ctx, vc)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reconciling queue %s: %w", q, err))
		}
		res[fmt.Sprintf("%s_released", q)] = released
		res[fmt.Sprintf("%s_claimed", q)] = claimed
	}

	return res, errors.Join(errs...)
}
