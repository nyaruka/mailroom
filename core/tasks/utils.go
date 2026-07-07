package tasks

import (
	"context"
	"time"

	valkey "github.com/gomodule/redigo/redis"
)

// Tracker is a Valkey-backed set for tracking batch completion. It is initialized with the IDs of the batches via
// Init and then Done is called with each batch's ID as it completes. Done returns true only for the call that removes
// the last remaining ID - so unlike a simple counter, duplicate completions of the same batch (e.g. a redelivered
// task) don't cause completion to be reported early or more than once.
type Tracker struct {
	key string
	ttl time.Duration
}

// NewTracker creates a new tracker with the given key and TTL.
func NewTracker(key string, ttl time.Duration) *Tracker {
	return &Tracker{key: key, ttl: ttl}
}

// Init resets the tracker to contain the given batch IDs with the configured TTL.
func (t *Tracker) Init(ctx context.Context, vk *valkey.Pool, ids []string) error {
	vc := vk.Get()
	defer vc.Close()

	args := valkey.Args{}.Add(t.key).Add(int(t.ttl.Seconds())).AddFlat(ids)
	_, err := trackerInit.DoContext(ctx, vc, args...)
	return err
}

// Done marks the batch with the given ID as complete and returns true if it was the last one remaining. Marking an
// already completed or unknown batch as complete is a no-op that returns false. The TTL is reset whenever batches
// remain to prevent orphaned keys.
func (t *Tracker) Done(ctx context.Context, vk *valkey.Pool, id string) (bool, error) {
	vc := vk.Get()
	defer vc.Close()

	vals, err := valkey.Ints(trackerDone.DoContext(ctx, vc, t.key, id, int(t.ttl.Seconds())))
	if err != nil {
		return false, err
	}

	removed, remaining := vals[0], vals[1]
	return removed == 1 && remaining == 0, nil
}

// Remaining returns the number of batches not yet completed, or 0 if the key doesn't exist.
func (t *Tracker) Remaining(ctx context.Context, vk *valkey.Pool) (int, error) {
	vc := vk.Get()
	defer vc.Close()

	return valkey.Int(valkey.DoContext(vc, ctx, "SCARD", t.key))
}

// Clear deletes the tracker key.
func (t *Tracker) Clear(ctx context.Context, vk *valkey.Pool) error {
	vc := vk.Get()
	defer vc.Close()

	_, err := valkey.DoContext(vc, ctx, "DEL", t.key)
	return err
}

var trackerInit = valkey.NewScript(1, `
redis.call('DEL', KEYS[1])
for i = 2, #ARGV do
	redis.call('SADD', KEYS[1], ARGV[i])
end
redis.call('EXPIRE', KEYS[1], ARGV[1])
return 1
`)

var trackerDone = valkey.NewScript(1, `
local removed = redis.call('SREM', KEYS[1], ARGV[1])
local remaining = redis.call('SCARD', KEYS[1])
if remaining > 0 then
	redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return {removed, remaining}
`)
