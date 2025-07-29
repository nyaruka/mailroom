package runtime

import "github.com/nyaruka/mailroom/utils/queues"

type Queues struct {
	Realtime  queues.Fair
	Batch     queues.Fair
	Throttled queues.Fair
}

func NewQueues(cfg *Config) *Queues {
	// all queues are configured to allow a single owner to use up to half the workers
	return &Queues{
		Realtime:  queues.NewFair("tasks:realtime", cfg.WorkersRealtime/2),
		Batch:     queues.NewFair("tasks:batch", cfg.WorkersBatch/2),
		Throttled: queues.NewFair("tasks:throttled", cfg.WorkersThrottled/2),
	}
}
