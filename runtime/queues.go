package runtime

import (
	"time"

	"github.com/nyaruka/mailroom/v26/utils/queues"
)

const (
	// popped tasks are leased for longer than the maximum task timeout so that they are only ever redelivered if
	// the worker that was processing them died
	taskLease = 65 * time.Minute

	// tasks delivered this many times without completing are assumed to be killing their workers and are moved to
	// the dead list
	taskMaxAttempts = 3
)

type Queues struct {
	Realtime  *queues.Fair
	Batch     *queues.Fair
	Throttled *queues.Fair
}

func newQueues(cfg *Config) *Queues {
	// all queues are configured to allow a single owner to use up to half the workers
	return &Queues{
		Realtime:  queues.NewFair("realtime", int(float64(cfg.WorkersRealtime)*cfg.WorkerOwnerLimit), taskLease, taskMaxAttempts),
		Batch:     queues.NewFair("batch", int(float64(cfg.WorkersBatch)*cfg.WorkerOwnerLimit), taskLease, taskMaxAttempts),
		Throttled: queues.NewFair("throttled", int(float64(cfg.WorkersThrottled)*cfg.WorkerOwnerLimit), taskLease, taskMaxAttempts),
	}
}
