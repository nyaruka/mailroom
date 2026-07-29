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

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	tracker := tasks.NewBatchTracker("7de40d16-0286-4938-be6b-9974e12e1b0f")

	err := tracker.Init(ctx, rt.VK, 3)
	assert.NoError(t, err)

	ttl, err := valkey.Int(vc.Do("TTL", "task_batches:7de40d16-0286-4938-be6b-9974e12e1b0f"))
	assert.NoError(t, err)
	assert.Greater(t, ttl, 0)

	// first batch to complete
	completed, total, err := tracker.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)
	assert.Equal(t, 3, total)

	// completing the same batch again changes nothing
	completed, total, err = tracker.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)
	assert.Equal(t, 3, total)

	completed, total, err = tracker.Done(ctx, rt.VK, "01981fa0-0002-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 2, completed)
	assert.Equal(t, 3, total)

	// last batch to complete
	completed, total, err = tracker.Done(ctx, rt.VK, "01981fa0-0003-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 3, completed)
	assert.Equal(t, 3, total)

	// completing a batch on an uninitialized (or expired) tracker reports a zero total
	other := tasks.NewBatchTracker("50d61890-a760-4c00-bd85-c60bb0a5e5b7")
	completed, total, err = other.Done(ctx, rt.VK, "01981fa0-0004-7000-8000-000000000000")
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)
	assert.Equal(t, 0, total)
}
