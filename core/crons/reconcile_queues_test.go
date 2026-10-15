package crons_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileQueues(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	cron := &crons.ReconcileQueuesCron{}
	res, err := cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"realtime_released": 0, "realtime_claimed": 0,
		"batch_released": 0, "batch_claimed": 0,
		"throttled_released": 0, "throttled_claimed": 0,
	}, res)

	_, err = rt.Queues.Batch.Push(ctx, vc, "type1", 1, "task1", false)
	require.NoError(t, err)
	task, err := rt.Queues.Batch.Pop(ctx, vc)
	require.NoError(t, err)

	// simulate slots held without leases, e.g. by workers of older versions which died, and a lost count
	_, err = vc.Do("ZADD", "{tasks:batch}:active", 3, "2")
	require.NoError(t, err)
	_, err = vc.Do("ZREM", "{tasks:batch}:active", "1")
	require.NoError(t, err)

	res, err = cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"realtime_released": 0, "realtime_claimed": 0,
		"batch_released": 3, "batch_claimed": 1,
		"throttled_released": 0, "throttled_claimed": 0,
	}, res)

	assertvk.ZGetAll(t, vc, "{tasks:batch}:active", map[string]float64{"1": 1})

	require.NoError(t, rt.Queues.Batch.Done(ctx, vc, task))

	assertvk.ZGetAll(t, vc, "{tasks:batch}:active", map[string]float64{})
}
