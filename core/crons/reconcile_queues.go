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

type ReconcileQueuesCron struct {
}

func (c *ReconcileQueuesCron) Next(last time.Time) time.Time {
	return Next(last, time.Minute*5)
}

// Run reconciles the active counts of the task queues, healing any drift left behind by workers which died
func (c *ReconcileQueuesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	vc := rt.VK.Get()
	defer vc.Close()

	var errs []error
	for _, q := range []*queues.Fair{rt.Queues.Realtime, rt.Queues.Batch, rt.Queues.Throttled} {
		if err := q.Reconcile(ctx, vc); err != nil {
			errs = append(errs, fmt.Errorf("error reconciling queue %s: %w", q, err))
		}
	}

	return map[string]any{}, errors.Join(errs...)
}
