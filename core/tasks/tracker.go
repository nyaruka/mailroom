package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/models"
)

const (
	batchTrackerKeyBase = "batch_task"

	// sorted set of the owner UUIDs of sets which haven't finished, scored by when they last made progress
	batchTrackerIndexKey = "batch_tasks"

	// how long tracker keys survive without progress - refreshed on every completion, so this bounds the gap
	// between batches of a set completing (which for a throttled org is queue wait) rather than how long the
	// set as a whole takes
	batchTrackerTTL = 24 * time.Hour
)

// BatchInfo describes a set of batch tasks. It's recorded when the set is queued so that the set can be displayed
// without having to look up its owner.
type BatchInfo struct {
	Type     string       `json:"type"`
	OrgID    models.OrgID `json:"org_id"`
	OrgName  string       `json:"org_name"`
	Label    string       `json:"label,omitempty"` // name of the owning asset where the set has one, e.g. a flow
	Total    int          `json:"total"`
	QueuedOn time.Time    `json:"queued_on"`
}

// BatchStatus is the progress of a set of batch tasks.
type BatchStatus struct {
	OwnerUUID uuids.UUID `json:"owner_uuid"`
	Info      *BatchInfo `json:"info"`      // nil if the set's info has expired or was never recorded
	Started   bool       `json:"started"`   // whether any of its batches has begun processing
	Completed int        `json:"completed"` // how many of its batches have finished
	LastOn    time.Time  `json:"last_on"`   // when it last made progress
}

// BatchTracker tracks progress of a set of batch tasks split out from an owning task or object, using three Valkey
// keys: a simple key marking that processing of the set has started, a hash of the task IDs of completed batches, and
// a key describing the set. Because completions are recorded as distinct hash fields, marking the same batch as done
// more than once has no effect, and the completed count can't reach the total until all batches are really done. All
// three keys have their TTL refreshed as batches complete, so a set only loses its tracking if it makes no progress
// at all for the length of the TTL.
//
// Sets which haven't finished are also held in a single index, so that they can be listed without scanning the
// keyspace - see GetBatchTasks.
type BatchTracker struct {
	ownerUUID  uuids.UUID
	startedKey string
	batchesKey string
	infoKey    string
}

// NewBatchTracker creates a tracker for the batches owned by the object or task with the given UUID.
func NewBatchTracker(ownerUUID uuids.UUID) *BatchTracker {
	return &BatchTracker{
		ownerUUID:  ownerUUID,
		startedKey: fmt.Sprintf("%s:%s:started", batchTrackerKeyBase, ownerUUID),
		batchesKey: fmt.Sprintf("%s:%s:batches", batchTrackerKeyBase, ownerUUID),
		infoKey:    fmt.Sprintf("%s:%s:info", batchTrackerKeyBase, ownerUUID),
	}
}

// Queued records that the set has been queued, making it visible as in-flight work before any of its batches run -
// which for a throttled org can be a long time.
func (t *BatchTracker) Queued(ctx context.Context, vk *valkey.Pool, info *BatchInfo) error {
	vc := vk.Get()
	defer vc.Close()

	now := dates.Now()

	_, err := trackerQueued.DoContext(ctx, vc, t.infoKey, batchTrackerIndexKey,
		jsonx.MustMarshal(info), int(batchTrackerTTL/time.Second), now.Unix(), string(t.ownerUUID), cutoff(now),
	)
	return err
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

// Done records the batch with the given task ID as complete and returns the number of completed batches. The total
// number of batches in the set is passed so that a set which is finished can be dropped from the index.
func (t *BatchTracker) Done(ctx context.Context, vk *valkey.Pool, taskID TaskID, total int) (int, error) {
	vc := vk.Get()
	defer vc.Close()

	return valkey.Int(trackerDone.DoContext(ctx, vc, t.batchesKey, t.startedKey, t.infoKey, batchTrackerIndexKey,
		string(taskID), int(batchTrackerTTL/time.Second), dates.Now().Unix(), string(t.ownerUUID), total,
	))
}

// records the set as queued and adds it to the index, trimming sets which are older than the TTL and so can no
// longer have any state of their own
var trackerQueued = valkey.NewScript(2, `
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[4])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', ARGV[5])
`)

// records a batch as complete and extends the lifetime of the set's other keys - if the started key were allowed to
// expire while batches were still being processed, a later batch would think it was the first to start. The set is
// rescored in the index so that it's ordered by when it last made progress, or dropped from it once it's finished.
var trackerDone = valkey.NewScript(4, `
redis.call('HSET', KEYS[1], ARGV[1], 1)
redis.call('EXPIRE', KEYS[1], ARGV[2])
redis.call('EXPIRE', KEYS[2], ARGV[2])
redis.call('EXPIRE', KEYS[3], ARGV[2])

local completed = redis.call('HLEN', KEYS[1])
local total = tonumber(ARGV[5])

if total > 0 and completed >= total then
    redis.call('ZREM', KEYS[4], ARGV[4])
else
    redis.call('ZADD', KEYS[4], ARGV[3], ARGV[4])
end

return completed
`)

// GetBatchTasks returns the sets of batch tasks which haven't finished, least recently active first - so the sets
// which have gone longest without making progress come first.
func GetBatchTasks(ctx context.Context, vk *valkey.Pool, limit int) ([]*BatchStatus, error) {
	vc := vk.Get()
	defer vc.Close()

	// sets which can no longer have any state of their own shouldn't still be listed as in-flight
	if _, err := valkey.DoContext(vc, ctx, "ZREMRANGEBYSCORE", batchTrackerIndexKey, "-inf", cutoff(dates.Now())); err != nil {
		return nil, fmt.Errorf("error trimming batch task index: %w", err)
	}

	entries, err := valkey.Values(valkey.DoContext(vc, ctx, "ZRANGE", batchTrackerIndexKey, 0, limit-1, "WITHSCORES"))
	if err != nil {
		return nil, fmt.Errorf("error reading batch task index: %w", err)
	}

	statuses := make([]*BatchStatus, 0, len(entries)/2)

	for i := 0; i+1 < len(entries); i += 2 {
		ownerUUID, err := valkey.String(entries[i], nil)
		if err != nil {
			return nil, fmt.Errorf("error reading batch task owner UUID: %w", err)
		}
		lastOn, err := valkey.Int64(entries[i+1], nil)
		if err != nil {
			return nil, fmt.Errorf("error reading batch task score: %w", err)
		}

		statuses = append(statuses, &BatchStatus{OwnerUUID: uuids.UUID(ownerUUID), LastOn: time.Unix(lastOn, 0).UTC()})
	}

	// fetch the state of every set in a single round trip
	for _, s := range statuses {
		t := NewBatchTracker(s.OwnerUUID)

		vc.Send("GET", t.infoKey)
		vc.Send("EXISTS", t.startedKey)
		vc.Send("HLEN", t.batchesKey)
	}
	if err := vc.Flush(); err != nil {
		return nil, fmt.Errorf("error requesting batch task states: %w", err)
	}

	for _, s := range statuses {
		infoJSON, err := valkey.Bytes(valkey.ReceiveContext(vc, ctx))
		if err != nil && err != valkey.ErrNil {
			return nil, fmt.Errorf("error reading info for batch task %s: %w", s.OwnerUUID, err)
		}
		started, err := valkey.Bool(valkey.ReceiveContext(vc, ctx))
		if err != nil {
			return nil, fmt.Errorf("error reading started for batch task %s: %w", s.OwnerUUID, err)
		}
		completed, err := valkey.Int(valkey.ReceiveContext(vc, ctx))
		if err != nil {
			return nil, fmt.Errorf("error reading completed for batch task %s: %w", s.OwnerUUID, err)
		}

		if len(infoJSON) > 0 {
			info := &BatchInfo{}
			if err := json.Unmarshal(infoJSON, info); err != nil {
				return nil, fmt.Errorf("error unmarshaling info for batch task %s: %w", s.OwnerUUID, err)
			}
			s.Info = info
		}

		s.Started = started
		s.Completed = completed
	}

	return statuses, nil
}

// the index score below which a set has no state of its own left and so shouldn't still be listed
func cutoff(now time.Time) int64 {
	return now.Add(-batchTrackerTTL).Unix()
}
