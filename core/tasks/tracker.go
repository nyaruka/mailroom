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
	legacyKey  string
}

// NewBatchTracker creates a tracker for the batches owned by the object or task with the given UUID.
func NewBatchTracker(ownerUUID uuids.UUID) *BatchTracker {
	return &BatchTracker{
		startedKey: fmt.Sprintf("%s:%s:started", batchTrackerKeyBase, ownerUUID),
		batchesKey: fmt.Sprintf("%s:%s:batches", batchTrackerKeyBase, ownerUUID),
		legacyKey:  fmt.Sprintf("task_batches:%s", ownerUUID), // TODO: remove once all sets tracked under it have drained
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

	return valkey.Int(trackerDone.DoContext(ctx, vc, t.batchesKey, t.legacyKey, string(taskID), int(batchTrackerTTL/time.Second)))
}

// completions recorded under the pre-rename key (a hash of task IDs plus a 'total' field) are counted too, so that
// sets in flight when the rename deployed still complete - and its TTL is refreshed so it outlives even sets that
// take days to drain from the queue
var trackerDone = valkey.NewScript(2, `
redis.call('HSET', KEYS[1], ARGV[1], 1)
redis.call('EXPIRE', KEYS[1], ARGV[2])
local completed = redis.call('HLEN', KEYS[1])
if redis.call('EXISTS', KEYS[2]) == 1 then
	redis.call('EXPIRE', KEYS[2], ARGV[2])
	completed = completed + redis.call('HLEN', KEYS[2]) - redis.call('HEXISTS', KEYS[2], 'total')
end
return completed
`)
