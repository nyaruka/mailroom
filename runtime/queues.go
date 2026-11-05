package runtime

import (
	"time"

	gqueues "github.com/nyaruka/gocommon/queues"
	"github.com/nyaruka/mailroom/v26/utils/queues"
)

// taskLease is how long a popped task holds its org's slot without its worker extending the lease, before it's assumed
// lost and the slot released
const taskLease = 5 * time.Minute

type Queues struct {
	Realtime  queues.Fair
	Batch     queues.Fair
	Throttled queues.Fair

	// courier pops from this queue, so its key base has to match courier's
	Courier *gqueues.MsgsV2
}

func newQueues(cfg *Config) *Queues {
	// all queues are configured to allow a single owner to use up to half the workers
	return &Queues{
		Realtime:  queues.NewFair("realtime", int(float64(cfg.WorkersRealtime)*cfg.WorkerOwnerLimit), taskLease),
		Batch:     queues.NewFair("batch", int(float64(cfg.WorkersBatch)*cfg.WorkerOwnerLimit), taskLease),
		Throttled: queues.NewFair("throttled", int(float64(cfg.WorkersThrottled)*cfg.WorkerOwnerLimit), taskLease),
		Courier:   gqueues.NewMsgsV2("msgs"),
	}
}
