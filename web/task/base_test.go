package task_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestBatches(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 6, 12, 14, 12, 0, 0, time.UTC), time.Second))
	defer dates.SetNowFunc(time.Now)

	tracker1 := tasks.NewBatchTracker("11111111-0000-7000-8000-000000000000")
	tracker2 := tasks.NewBatchTracker("22222222-0000-7000-8000-000000000000")

	require.NoError(t, tracker1.Queued(ctx, rt.VK, &tasks.BatchInfo{
		Type: tasks.TypeStartFlowBatch, OrgID: testdb.Org1.ID, Label: "Favorites", Total: 3, QueuedOn: dates.Now(),
	}))
	require.NoError(t, tracker2.Queued(ctx, rt.VK, &tasks.BatchInfo{
		Type: tasks.TypeImportContactBatch, OrgID: testdb.Org2.ID, Total: 2, QueuedOn: dates.Now(),
	}))

	_, err := tracker2.Started(ctx, rt.VK)
	require.NoError(t, err)
	_, err = tracker2.Done(ctx, rt.VK, "01981fa0-0001-7000-8000-000000000000", 2)
	require.NoError(t, err)

	testsuite.RunWebTests(t, rt, "testdata/batches.json")
}
