package tasks

import (
	"context"
	"fmt"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/uuids"
)

const (
	batchTrackerKeyBase = "task_batches"
	batchTrackerTTL     = 24 * time.Hour
)

// BatchTracker is a Valkey-backed hash for tracking completion of a set of batch tasks split out from an owning task
// or object. The owner initializes it with the total number of batches before queuing them, and each batch then calls
// Done with its own task ID as it completes. Because completions are recorded as distinct hash fields, marking the
// same batch as done more than once has no effect, and completion can't be reported before all batches are really done.
type BatchTracker struct {
	key string
}

// NewBatchTracker creates a tracker for the batches owned by the object or task with the given UUID.
func NewBatchTracker(ownerUUID uuids.UUID) *BatchTracker {
	return &BatchTracker{key: fmt.Sprintf("%s:%s", batchTrackerKeyBase, ownerUUID)}
}

// Init records the total number of batches, and must be called before any of them are queued.
func (t *BatchTracker) Init(ctx context.Context, vk *valkey.Pool, total int) error {
	vc := vk.Get()
	defer vc.Close()

	_, err := trackerInit.DoContext(ctx, vc, t.key, total, int(batchTrackerTTL/time.Second))
	return err
}

// Done records the batch with the given task ID as complete and returns the number of completed batches and the total,
// so the caller can tell if it was the first (completed == 1) or last (completed >= total) to complete. A total of
// zero means the tracker was never initialized or has expired.
func (t *BatchTracker) Done(ctx context.Context, vk *valkey.Pool, taskID TaskID) (int, int, error) {
	vc := vk.Get()
	defer vc.Close()

	vals, err := valkey.Ints(trackerDone.DoContext(ctx, vc, t.key, string(taskID), int(batchTrackerTTL/time.Second)))
	if err != nil {
		return 0, 0, err
	}

	return vals[0], vals[1], nil
}

var trackerInit = valkey.NewScript(1, `
redis.call('HSET', KEYS[1], 'total', ARGV[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
return 1
`)

var trackerDone = valkey.NewScript(1, `
redis.call('HSET', KEYS[1], ARGV[1], 1)
redis.call('EXPIRE', KEYS[1], ARGV[2])
local hasTotal = redis.call('HEXISTS', KEYS[1], 'total')
local completed = redis.call('HLEN', KEYS[1]) - hasTotal
local total = tonumber(redis.call('HGET', KEYS[1], 'total')) or 0
return {completed, total}
`)
