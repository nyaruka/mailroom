package tasks

import (
	"context"
	"fmt"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/uuids"
)

const (
	batchTrackerKeyBase = "batch_task"
	batchTrackerTTL     = 24 * time.Hour
)

// BatchTracker tracks progress of a set of batch tasks split out from an owning task or object, using two Valkey keys:
// a simple key marking that processing of the set has started, and a hash of the task IDs of completed batches. Because
// completions are recorded as distinct hash fields, marking the same batch as done more than once has no effect, and
// the completed count can't reach the total until all batches are really done.
type BatchTracker struct {
	startedKey string
	batchesKey string
}

// NewBatchTracker creates a tracker for the batches owned by the object or task with the given UUID.
func NewBatchTracker(ownerUUID uuids.UUID) *BatchTracker {
	return &BatchTracker{
		startedKey: fmt.Sprintf("%s:%s:started", batchTrackerKeyBase, ownerUUID),
		batchesKey: fmt.Sprintf("%s:%s:batches", batchTrackerKeyBase, ownerUUID),
	}
}

// Started records that processing of the set has started and returns whether this was the first batch to do so.
func (t *BatchTracker) Started(ctx context.Context, vk *valkey.Pool) (bool, error) {
	vc := vk.Get()
	defer vc.Close()

	reply, err := valkey.DoContext(vc, ctx, "SET", t.startedKey, 1, "NX", "EX", int(batchTrackerTTL/time.Second))
	if err != nil {
		return false, err
	}

	return reply != nil, nil // SET .. NX returns nil if key already existed
}

// Done records the batch with the given task ID as complete and returns the number of completed batches.
func (t *BatchTracker) Done(ctx context.Context, vk *valkey.Pool, taskID TaskID) (int, error) {
	vc := vk.Get()
	defer vc.Close()

	return valkey.Int(trackerDone.DoContext(ctx, vc, t.batchesKey, string(taskID), int(batchTrackerTTL/time.Second)))
}

var trackerDone = valkey.NewScript(1, `
redis.call('HSET', KEYS[1], ARGV[1], 1)
redis.call('EXPIRE', KEYS[1], ARGV[2])
return redis.call('HLEN', KEYS[1])
`)
