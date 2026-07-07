package crons_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/testsuite"
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

	cron := &crons.ReconcileQueuesCron{}
	res, err := cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, res)

	require.NoError(t, rt.Queues.Realtime.Done(ctx, vc, task.ID))
}
