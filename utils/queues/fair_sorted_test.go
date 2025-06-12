package queues_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueues(t *testing.T) {
	_, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2022, 1, 1, 12, 1, 2, 123456789, time.UTC), time.Second))
	defer dates.SetNowFunc(time.Now)

	defer testsuite.Reset(testsuite.ResetRedis)

	var q queues.Fair = queues.NewFairSorted("test")
	assert.Equal(t, "test", fmt.Sprint(q))

	assertPop := func(expectedOwnerID int, expectedBody string) {
		task, err := q.Pop(rc)
		require.NoError(t, err)
		if expectedBody != "" {
			assert.Equal(t, expectedOwnerID, task.OwnerID)
			assert.Equal(t, expectedBody, string(task.Task))
		} else {
			assert.Nil(t, task)
		}
	}

	assertSize := func(expecting int) {
		size, err := q.Size(rc)
		assert.NoError(t, err)
		assert.Equal(t, expecting, size)
	}

	assertOwners := func(expected []int) {
		actual, err := q.Owners(rc)
		assert.NoError(t, err)
		assert.ElementsMatch(t, expected, actual)
	}

	assertSize(0)

	q.Push(rc, "type1", 1, "task1", false)
	q.Push(rc, "type1", 1, "task2", true)
	q.Push(rc, "type1", 2, "task3", false)
	q.Push(rc, "type2", 1, "task4", false)
	q.Push(rc, "type2", 2, "task5", true)

	// nobody processing any tasks so no workers assigned in active set
	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 0, "2": 0})

	assertvk.ZGetAll(t, rc, "test:1", map[string]float64{
		`{"type":"type1","task":"task2","queued_on":"2022-01-01T12:01:05.123456789Z"}`: 1631038464.123456,
		`{"type":"type1","task":"task1","queued_on":"2022-01-01T12:01:03.123456789Z"}`: 1641038462.123456,
		`{"type":"type2","task":"task4","queued_on":"2022-01-01T12:01:09.123456789Z"}`: 1641038468.123456,
	})
	assertvk.ZGetAll(t, rc, "test:2", map[string]float64{
		`{"type":"type1","task":"task3","queued_on":"2022-01-01T12:01:07.123456789Z"}`: 1641038466.123456,
		`{"type":"type2","task":"task5","queued_on":"2022-01-01T12:01:11.123456789Z"}`: 1631038470.123456,
	})

	assertSize(5)

	assertPop(1, `"task2"`) // because it's highest priority for owner 1
	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 1, "2": 0})
	assertPop(2, `"task5"`) // because it's highest priority for owner 2
	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 1, "2": 1})
	assertPop(1, `"task1"`)
	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 2, "2": 1})

	assertOwners([]int{1, 2})
	assertSize(2)

	// mark task2 and task1 (owner 1) as complete
	q.Done(rc, 1)
	q.Done(rc, 1)

	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 0, "2": 1})

	assertPop(1, `"task4"`)
	assertPop(2, `"task3"`)
	assertPop(0, "") // no more tasks

	assertSize(0)

	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{})

	q.Push(rc, "type1", 1, "task6", false)
	q.Push(rc, "type1", 1, "task7", false)
	q.Push(rc, "type1", 2, "task8", false)
	q.Push(rc, "type1", 2, "task9", false)

	assertPop(1, `"task6"`)

	q.Pause(rc, 1)
	q.Pause(rc, 1) // no-op if already paused

	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 1000001, "2": 0})
	assertOwners([]int{1, 2})

	assertPop(2, `"task8"`)
	assertPop(2, `"task9"`)
	assertPop(0, "") // no more tasks

	q.Resume(rc, 1)
	q.Resume(rc, 1) // no-op if already active

	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 1})
	assertOwners([]int{1})

	assertPop(1, `"task7"`)

	q.Done(rc, 1)
	q.Done(rc, 1)
	q.Done(rc, 2)
	q.Done(rc, 2)

	// if we somehow get into a state where an owner is in the active set but doesn't have queued tasks, pop will retry
	q.Push(rc, "type1", 1, "task6", false)
	q.Push(rc, "type1", 2, "task7", false)

	rc.Do("ZREMRANGEBYRANK", "test:1", 0, 1)

	assertPop(2, `"task7"`)
	assertPop(0, "")

	// if we somehow call done too many times, we never get negative workers
	q.Push(rc, "type1", 1, "task8", false)
	q.Done(rc, 1)
	q.Done(rc, 1)

	assertvk.ZGetAll(t, rc, "test:active", map[string]float64{"1": 0})
}
