package queues_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/utils/queues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFair(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2022, 1, 1, 12, 1, 2, 123456789, time.UTC), time.Second))
	defer dates.SetNowFunc(time.Now)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	q := queues.NewFair("test", 10, time.Minute*5, 3)
	assert.Equal(t, "test", fmt.Sprint(q))

	assertPop := func(expectedOwnerID int, expectedBody string) *queues.Task {
		task, err := q.Pop(ctx, vc)
		require.NoError(t, err)
		if expectedBody != "" {
			assert.Equal(t, expectedOwnerID, task.OwnerID)
			assert.Equal(t, expectedBody, string(task.Task))
			assert.Equal(t, 1, task.Attempts)
		} else {
			assert.Nil(t, task)
		}
		return task
	}

	assertSize := func(expecting int) {
		size, err := q.Size(ctx, vc)
		assert.NoError(t, err)
		assert.Equal(t, expecting, size)
	}

	assertOwners := func(expected []int) {
		actual, err := q.Queued(ctx, vc)
		assert.NoError(t, err)
		assert.ElementsMatch(t, expected, actual)
	}

	assertSize(0)

	q.Push(ctx, vc, "type1", 1, "task1", false)
	q.Push(ctx, vc, "type1", 1, "task2", true)
	q.Push(ctx, vc, "type1", 2, "task3", false)
	q.Push(ctx, vc, "type2", 1, "task4", false)
	q.Push(ctx, vc, "type2", 2, "task5", true)

	assertSize(5)

	task2 := assertPop(1, `"task2"`) // because it's highest priority for owner 1
	task5 := assertPop(2, `"task5"`) // because it's highest priority for owner 2
	task1 := assertPop(1, `"task1"`)

	assertOwners([]int{1, 2})
	assertSize(2)

	// mark task2 and task1 (owner 1) as complete
	require.NoError(t, q.Done(ctx, vc, task2.ID))
	require.NoError(t, q.Done(ctx, vc, task1.ID))

	task4 := assertPop(1, `"task4"`)
	task3 := assertPop(2, `"task3"`)
	assertPop(0, "") // no more tasks

	assertSize(0)

	require.NoError(t, q.Done(ctx, vc, task5.ID))
	require.NoError(t, q.Done(ctx, vc, task4.ID))
	require.NoError(t, q.Done(ctx, vc, task3.ID))

	q.Push(ctx, vc, "type1", 1, "task6", false)
	q.Push(ctx, vc, "type1", 1, "task7", false)
	q.Push(ctx, vc, "type1", 2, "task8", false)
	q.Push(ctx, vc, "type1", 2, "task9", false)

	task6 := assertPop(1, `"task6"`)

	q.Pause(ctx, vc, 1)
	q.Pause(ctx, vc, 1) // no-op if already paused

	assertOwners([]int{1, 2})

	task8 := assertPop(2, `"task8"`)
	task9 := assertPop(2, `"task9"`)
	assertPop(0, "") // no more tasks

	q.Resume(ctx, vc, 1)
	q.Resume(ctx, vc, 1) // no-op if already active

	assertOwners([]int{1})

	task7 := assertPop(1, `"task7"`)

	require.NoError(t, q.Done(ctx, vc, task6.ID))
	require.NoError(t, q.Done(ctx, vc, task7.ID))
	require.NoError(t, q.Done(ctx, vc, task8.ID))
	require.NoError(t, q.Done(ctx, vc, task9.ID))

	require.NoError(t, q.Reconcile(ctx, vc))
}
