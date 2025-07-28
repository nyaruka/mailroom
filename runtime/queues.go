package runtime

import "github.com/nyaruka/mailroom/utils/queues"

type Queues struct {
	Handler   queues.Fair
	BatchOld  queues.Fair
	Batch     queues.Fair
	Throttled queues.Fair
}

func NewQueues(cfg *Config) *Queues {
	return &Queues{
		Handler:   queues.NewFairSorted("tasks:handler"),
		BatchOld:  queues.NewFairSorted("tasks:batch"),
		Batch:     queues.NewFairV2("tasks:batch", cfg.BatchWorkers/2),
		Throttled: queues.NewFairSorted("tasks:throttled"),
	}
}
