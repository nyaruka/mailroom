package tasks_test

import (
	"testing"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchTracker(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	const startedKey = "batch_task:7de40d16-0286-4938-be6b-9974e12e1b0f:started"
	const batchesKey = "batch_task:7de40d16-0286-4938-be6b-9974e12e1b0f:batches"

	ttlOf := func(key string) int {
		ttl, err := valkey.Int(vc.Do("TTL", key))
		require.NoError(t, err)
		return ttl
	}

	tracker := tasks.NewBatchTracker("7de40d16-0286-4938-be6b-9974e12e1b0f")

	// first batch to start
	first, err := tracker.Started(ctx, rt.VK)
	assert.NoError(t, err)
	assert.True(t, first)

	assert.Greater(t, ttlOf(startedKey), 0)

	// subsequent batches aren't first
	first, err = tracker.Started(ctx, rt.VK)
	assert.NoError(t, err)
	assert.False(t, first)

	// first batch to complete
	completed, err := tracker.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)

	assert.Greater(t, ttlOf(batchesKey), 0)

	// completing the same batch again changes nothing
	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)

	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0002-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 2, completed)

	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0003-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 3, completed)

	// simulate a set which has made no progress for a while by shortening the TTLs of both keys
	vc.Do("EXPIRE", startedKey, 60)
	vc.Do("EXPIRE", batchesKey, 60)

	// completing another batch refreshes both, so that a set only loses its tracking if it stalls completely
	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0004-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 4, completed)

	assert.Greater(t, ttlOf(startedKey), 60)
	assert.Greater(t, ttlOf(batchesKey), 60)

	// so a later batch never thinks it's the first to start
	first, err = tracker.Started(ctx, rt.VK)
	assert.NoError(t, err)
	assert.False(t, first)

	// but a set which stalls for longer than the TTL loses its tracking rather than having it resurrected
	vc.Do("DEL", startedKey)

	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0005-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 5, completed)

	exists, err := valkey.Int(vc.Do("EXISTS", startedKey))
	assert.NoError(t, err)
	assert.Equal(t, 0, exists)
}
