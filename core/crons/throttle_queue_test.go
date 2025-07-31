package crons_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThrottleQueue(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetValkey|testsuite.ResetData)

	cron := &crons.ThrottleQueueCron{}
	res, err := cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 0, "resumed": 0}, res)

	_, err = rt.Queues.Throttled.Push(ctx, vc, "type1", 1, "task1", false)
	require.NoError(t, err)

	res, err = cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 0, "resumed": 0}, res)

	// make it look like org 1 has 20,000 messages in its outbox
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES ($1, 'msgs:folder:O', 10050, FALSE)`, testdb.Org1.ID)

	models.FlushCache()

	res, err = cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 1, "resumed": 0}, res)

	// make it look like most of the inbox has cleared
	rt.DB.MustExec(`INSERT INTO orgs_itemcount(org_id, scope, count, is_squashed) VALUES ($1, 'msgs:folder:O', -10000, FALSE)`, testdb.Org1.ID)

	models.FlushCache()

	res, err = cron.Run(ctx, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"paused": 0, "resumed": 1}, res)
}
