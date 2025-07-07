package crons_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThrottleQueue(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.VK.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetValkey | testsuite.ResetData)

	queue := queues.NewFairSorted("test")
	cron := &crons.ThrottleQueueCron{Queue: queue}
	res, err := cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 0, "resumed": 0}, res)

	queue.Push(ctx, rc, "type1", 1, "task1", false)

	res, err = cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 0, "resumed": 1}, res)

	// make it look like org 1 has 20,000 messages in its outbox
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES (1, 'msgs:folder:O', 10050, FALSE)`)

	models.FlushCache()

	res, err = cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 1, "resumed": 0}, res)

	// make it look like most of the inbox has cleared
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES (1, 'msgs:folder:O', -10000, FALSE)`)

	models.FlushCache()

	res, err = cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 0, "resumed": 1}, res)
}
