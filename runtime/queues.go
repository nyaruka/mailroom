package runtime

import "github.com/nyaruka/mailroom/utils/queues"

type Queues struct {
	Realtime  queues.Fair
	Batch     queues.Fair
	Throttled queues.Fair
}

func newQueues(cfg *Config) *Queues {
	// all queues are configured to allow a single owner to use up to half the workers
	return &Queues{
		Realtime:  queues.NewFair("realtime", int(float64(cfg.WorkersRealtime)*cfg.WorkerOwnerLimit)),
		Batch:     queues.NewFair("batch", int(float64(cfg.WorkersBatch)*cfg.WorkerOwnerLimit)),
		Throttled: queues.NewFair("throttled", int(float64(cfg.WorkersThrottled)*cfg.WorkerOwnerLimit)),
	}
}
