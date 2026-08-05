package tasks_test

import (
	"testing"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
)

func TestBatchTracker(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	tracker := tasks.NewBatchTracker("7de40d16-0286-4938-be6b-9974e12e1b0f")

	// first batch to start
	first, err := tracker.Started(ctx, rt.VK)
	assert.NoError(t, err)
	assert.True(t, first)

	ttl, err := valkey.Int(vc.Do("TTL", "batch_task:7de40d16-0286-4938-be6b-9974e12e1b0f:started"))
	assert.NoError(t, err)
	assert.Greater(t, ttl, 0)

	// subsequent batches aren't first
	first, err = tracker.Started(ctx, rt.VK)
	assert.NoError(t, err)
	assert.False(t, first)

	// first batch to complete
	completed, err := tracker.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)

	ttl, err = valkey.Int(vc.Do("TTL", "batch_task:7de40d16-0286-4938-be6b-9974e12e1b0f:batches"))
	assert.NoError(t, err)
	assert.Greater(t, ttl, 0)

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
}
