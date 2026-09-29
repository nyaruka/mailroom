package runtime

import (
	"time"

	"github.com/nyaruka/mailroom/v26/utils/queues"
)

// taskLease is how long a popped task holds its org's slot before it's assumed lost and the slot released - it's set
// above the longest task timeout so that it only expires for tasks whose workers died or overran their timeouts
const taskLease = 65 * time.Minute

type Queues struct {
	Realtime  queues.Fair
	Batch     queues.Fair
	Throttled queues.Fair
}

func newQueues(cfg *Config) *Queues {
	// all queues are configured to allow a single owner to use up to half the workers
	return &Queues{
		Realtime:  queues.NewFair("realtime", int(float64(cfg.WorkersRealtime)*cfg.WorkerOwnerLimit), taskLease),
		Batch:     queues.NewFair("batch", int(float64(cfg.WorkersBatch)*cfg.WorkerOwnerLimit), taskLease),
		Throttled: queues.NewFair("throttled", int(float64(cfg.WorkersThrottled)*cfg.WorkerOwnerLimit), taskLease),
	}
}
