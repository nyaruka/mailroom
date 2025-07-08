package runtime

import "github.com/nyaruka/mailroom/utils/queues"

type Queues struct {
	Handler   queues.Fair
	Batch     queues.Fair
	Throttled queues.Fair
}

func NewQueues(cfg *Config) *Queues {
	return &Queues{
		Handler:   queues.NewFairSorted("tasks:handler"),
		Batch:     queues.NewFairSorted("tasks:batch"),
		Throttled: queues.NewFairSorted("tasks:throttled"),
	}
}
