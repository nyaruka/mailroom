package runtime

import "github.com/nyaruka/mailroom/utils/queues"

type Queues struct {
	Handler    queues.Fair
	HandlerOld queues.Fair
	Batch      queues.Fair
	Throttled  queues.Fair
}

func NewQueues(cfg *Config) *Queues {
	return &Queues{
		Handler:    queues.NewFair("tasks:handler", cfg.HandlerWorkers/2),
		HandlerOld: queues.NewFairLegacy("tasks:handler"),
		Batch:      queues.NewFair("tasks:batch", cfg.BatchWorkers/2),
		Throttled:  queues.NewFair("tasks:throttled", cfg.BatchWorkers/2),
	}
}
