package runtime

import "github.com/nyaruka/mailroom/utils/queues"

type Queues struct {
	Realtime  queues.Fair
	Batch     queues.Fair
	Throttled queues.Fair
	Handler   queues.Fair // TODO remove
}

func NewQueues(cfg *Config) *Queues {
	return &Queues{
		Realtime:  queues.NewFair("tasks:realtime", cfg.HandlerWorkers/2),
		Batch:     queues.NewFair("tasks:batch", cfg.BatchWorkers/2),
		Throttled: queues.NewFair("tasks:throttled", cfg.BatchWorkers/2),
		Handler:   queues.NewFairLegacy("tasks:handler"),
	}
}
