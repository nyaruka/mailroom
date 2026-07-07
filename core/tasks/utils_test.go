package tasks_test

import (
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
)

func TestTracker(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	tracker := tasks.NewTracker("test_tracker", time.Minute)

	// init tracker with 3 batches
	err := tracker.Init(ctx, rt.VK, []string{"1", "2", "3"})
	assert.NoError(t, err)

	members, err := valkey.Strings(vc.Do("SMEMBERS", "test_tracker"))
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"1", "2", "3"}, members)

	ttl, err := valkey.Int(vc.Do("TTL", "test_tracker"))
	assert.NoError(t, err)
	assert.Greater(t, ttl, 0)

	remaining, err := tracker.Remaining(ctx, rt.VK)
	assert.NoError(t, err)
	assert.Equal(t, 3, remaining)

	// completing first two batches should return false
	done, err := tracker.Done(ctx, rt.VK, "2")
	assert.NoError(t, err)
	assert.False(t, done)

	done, err = tracker.Done(ctx, rt.VK, "1")
	assert.NoError(t, err)
	assert.False(t, done)

	// completing an already completed batch is a no-op
	done, err = tracker.Done(ctx, rt.VK, "1")
	assert.NoError(t, err)
	assert.False(t, done)

	remaining, err = tracker.Remaining(ctx, rt.VK)
	assert.NoError(t, err)
	assert.Equal(t, 1, remaining)

	// completing the last batch should return true
	done, err = tracker.Done(ctx, rt.VK, "3")
	assert.NoError(t, err)
	assert.True(t, done)

	// and even it can't complete again
	done, err = tracker.Done(ctx, rt.VK, "3")
	assert.NoError(t, err)
	assert.False(t, done)

	remaining, err = tracker.Remaining(ctx, rt.VK)
	assert.NoError(t, err)
	assert.Equal(t, 0, remaining)

	// re-initializing replaces any previous batches
	err = tracker.Init(ctx, rt.VK, []string{"4"})
	assert.NoError(t, err)
	err = tracker.Init(ctx, rt.VK, []string{"5", "6"})
	assert.NoError(t, err)

	members, err = valkey.Strings(vc.Do("SMEMBERS", "test_tracker"))
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"5", "6"}, members)

	err = tracker.Clear(ctx, rt.VK)
	assert.NoError(t, err)

	remaining, err = tracker.Remaining(ctx, rt.VK)
	assert.NoError(t, err)
	assert.Equal(t, 0, remaining)
}
