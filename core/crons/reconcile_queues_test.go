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

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	_, err := rt.Queues.Realtime.Push(ctx, vc, "type1", 1, "task1", false)
	require.NoError(t, err)

	task, err := rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)

	// simulate drift left behind by dead workers: an inflated count for owner 1 and a count for an
	// owner with no in-flight tasks at all
	_, err = vc.Do("ZINCRBY", "{tasks:realtime}:active", 2, "1")
	require.NoError(t, err)
	_, err = vc.Do("ZADD", "{tasks:realtime}:active", 4, "99")
	require.NoError(t, err)

	cron := &crons.ReconcileQueuesCron{}
	res, err := cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, res)

	// active counts should be rebuilt from the in-flight records
	assertvk.ZGetAll(t, vc, "{tasks:realtime}:active", map[string]float64{"1": 1})

	require.NoError(t, rt.Queues.Realtime.Done(ctx, vc, task.ID))

	assertvk.ZGetAll(t, vc, "{tasks:realtime}:active", map[string]float64{})
}
