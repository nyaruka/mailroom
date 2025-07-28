package runtime

import "github.com/nyaruka/mailroom/utils/queues"

type Queues struct {
	Handler      queues.Fair
	Batch        queues.Fair
	Throttled    queues.Fair
	ThrottledOld queues.Fair
}

func NewQueues(cfg *Config) *Queues {
	return &Queues{
		Handler:      queues.NewFairSorted("tasks:handler"),
		Batch:        queues.NewFairV2("tasks:batch", cfg.BatchWorkers/2),
		Throttled:    queues.NewFairV2("tasks:throttled", cfg.BatchWorkers/2),
		ThrottledOld: queues.NewFairSorted("tasks:throttled"),
	}
}
