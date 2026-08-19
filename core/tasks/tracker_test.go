package tasks_test

import (
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchTracker(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 6, 12, 14, 12, 0, 0, time.UTC), time.Second))
	defer dates.SetNowFunc(time.Now)

	const startedKey = "batch_task:7de40d16-0286-4938-be6b-9974e12e1b0f:started"
	const batchesKey = "batch_task:7de40d16-0286-4938-be6b-9974e12e1b0f:batches"
	const infoKey = "batch_task:7de40d16-0286-4938-be6b-9974e12e1b0f:info"

	ttlOf := func(key string) int {
		ttl, err := valkey.Int(vc.Do("TTL", key))
		require.NoError(t, err)
		return ttl
	}

	tracker := tasks.NewBatchTracker("7de40d16-0286-4938-be6b-9974e12e1b0f")

	err := tracker.Queued(ctx, rt.VK, &tasks.BatchInfo{
		Type: tasks.TypeStartFlowBatch, OrgID: testdb.Org1.ID, OrgName: "TextIt", Label: "Favorites", Total: 5, QueuedOn: dates.Now(),
	})
	assert.NoError(t, err)

	assert.Greater(t, ttlOf(infoKey), 0)

	// queued but not yet started, so listed with no progress
	statuses, err := tasks.GetBatchTasks(ctx, rt.VK, 100)
	assert.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, tasks.TypeStartFlowBatch, statuses[0].Info.Type)
	assert.Equal(t, "TextIt", statuses[0].Info.OrgName)
	assert.Equal(t, "Favorites", statuses[0].Info.Label)
	assert.Equal(t, 5, statuses[0].Info.Total)
	assert.False(t, statuses[0].Started)
	assert.Equal(t, 0, statuses[0].Completed)

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
	completed, err := tracker.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000", 5)
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)

	assert.Greater(t, ttlOf(batchesKey), 0)

	// completing the same batch again changes nothing
	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000", 5)
	assert.NoError(t, err)
	assert.Equal(t, 1, completed)

	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0002-7000-8000-000000000000", 5)
	assert.NoError(t, err)
	assert.Equal(t, 2, completed)

	statuses, err = tasks.GetBatchTasks(ctx, rt.VK, 100)
	assert.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Started)
	assert.Equal(t, 2, statuses[0].Completed)

	// simulate a set which has made no progress for a while by shortening the TTLs of its keys
	vc.Do("EXPIRE", startedKey, 60)
	vc.Do("EXPIRE", batchesKey, 60)
	vc.Do("EXPIRE", infoKey, 60)

	// completing another batch refreshes all three, so that a set only loses its tracking if it stalls completely
	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0003-7000-8000-000000000000", 5)
	assert.NoError(t, err)
	assert.Equal(t, 3, completed)

	assert.Greater(t, ttlOf(startedKey), 60)
	assert.Greater(t, ttlOf(batchesKey), 60)
	assert.Greater(t, ttlOf(infoKey), 60)

	// so a later batch never thinks it's the first to start
	first, err = tracker.Started(ctx, rt.VK)
	assert.NoError(t, err)
	assert.False(t, first)

	// but a set which stalls for longer than the TTL loses its tracking rather than having it resurrected
	vc.Do("DEL", startedKey)

	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0004-7000-8000-000000000000", 5)
	assert.NoError(t, err)
	assert.Equal(t, 4, completed)

	exists, err := valkey.Int(vc.Do("EXISTS", startedKey))
	assert.NoError(t, err)
	assert.Equal(t, 0, exists)

	// completing the last batch removes the set from the index
	completed, err = tracker.Done(ctx, rt.VK, "01981fa0-0005-7000-8000-000000000000", 5)
	assert.NoError(t, err)
	assert.Equal(t, 5, completed)

	statuses, err = tasks.GetBatchTasks(ctx, rt.VK, 100)
	assert.NoError(t, err)
	assert.Len(t, statuses, 0)
}

func TestGetBatchTasks(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 6, 12, 14, 12, 0, 0, time.UTC), time.Second))
	defer dates.SetNowFunc(time.Now)

	tracker1 := tasks.NewBatchTracker("11111111-0000-7000-8000-000000000000")
	tracker2 := tasks.NewBatchTracker("22222222-0000-7000-8000-000000000000")
	tracker3 := tasks.NewBatchTracker("33333333-0000-7000-8000-000000000000")

	require.NoError(t, tracker1.Queued(ctx, rt.VK, &tasks.BatchInfo{Type: tasks.TypeSendBroadcastBatch, OrgID: testdb.Org1.ID, OrgName: "TextIt", Total: 2, QueuedOn: dates.Now()}))
	require.NoError(t, tracker2.Queued(ctx, rt.VK, &tasks.BatchInfo{Type: tasks.TypeStartFlowBatch, OrgID: testdb.Org2.ID, OrgName: "Nyaruka", Label: "Favorites", Total: 3, QueuedOn: dates.Now()}))
	require.NoError(t, tracker3.Queued(ctx, rt.VK, &tasks.BatchInfo{Type: tasks.TypeImportContactBatch, OrgID: testdb.Org1.ID, OrgName: "TextIt", Total: 4, QueuedOn: dates.Now()}))

	// completing a batch of the first set rescores it to most recently active
	_, err := tracker1.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000", 2)
	require.NoError(t, err)

	statuses, err := tasks.GetBatchTasks(ctx, rt.VK, 100)
	assert.NoError(t, err)
	require.Len(t, statuses, 3)

	// least recently active first
	assert.Equal(t, "22222222-0000-7000-8000-000000000000", string(statuses[0].OwnerUUID))
	assert.Equal(t, "33333333-0000-7000-8000-000000000000", string(statuses[1].OwnerUUID))
	assert.Equal(t, "11111111-0000-7000-8000-000000000000", string(statuses[2].OwnerUUID))
	assert.Equal(t, 1, statuses[2].Completed)

	// limit is respected
	statuses, err = tasks.GetBatchTasks(ctx, rt.VK, 2)
	assert.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.Equal(t, "22222222-0000-7000-8000-000000000000", string(statuses[0].OwnerUUID))

	// a set whose last activity is older than the TTL is trimmed from the index - its own keys have expired so
	// there's nothing left to show for it
	_, err = vc.Do("ZADD", "batch_tasks", time.Date(2025, 6, 10, 14, 12, 0, 0, time.UTC).Unix(), "44444444-0000-7000-8000-000000000000")
	require.NoError(t, err)

	statuses, err = tasks.GetBatchTasks(ctx, rt.VK, 100)
	assert.NoError(t, err)
	require.Len(t, statuses, 3)

	// a set still in the index whose info key has expired is listed without info rather than dropped
	vc.Do("DEL", "batch_task:22222222-0000-7000-8000-000000000000:info")

	statuses, err = tasks.GetBatchTasks(ctx, rt.VK, 100)
	assert.NoError(t, err)
	require.Len(t, statuses, 3)
	assert.Nil(t, statuses[0].Info)
	assert.NotNil(t, statuses[1].Info)
}
