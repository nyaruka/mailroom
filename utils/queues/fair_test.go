package queues

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFair(t *testing.T) {
	ctx := t.Context()
	vp := assertvk.ClaimDB(t).Pool()
	vc := vp.Get()
	defer vc.Close()

	numIDs := 0
	newTaskID = func() TaskID {
		numIDs++
		return TaskID(fmt.Sprintf("01980000-0000-7000-8000-%012d", numIDs))
	}
	defer func() { newTaskID = defaultNewTaskID }()

	q := newFair("test", 3, time.Minute)

	assertQueued := func(expected map[OwnerID]int) {
		actualStrings, err := valkey.StringMap(vc.Do("ZRANGE", "{test}:queued", 0, -1, "WITHSCORES"))
		require.NoError(t, err)

		actual := make(map[OwnerID]int, len(actualStrings))
		for k, v := range actualStrings {
			actual[OwnerID(k)], err = strconv.Atoi(v)
			require.NoError(t, err)
		}

		assert.Equal(t, expected, actual)

		// checked the .Queued method as well
		actualOwners, err := q.queued(ctx, vc)
		assert.NoError(t, err)
		assert.ElementsMatch(t, slices.Collect(maps.Keys(expected)), actualOwners)
	}

	assertActive := func(expected map[OwnerID]int) {
		actualStrings, err := valkey.StringMap(vc.Do("ZRANGE", "{test}:active", 0, -1, "WITHSCORES"))
		require.NoError(t, err)

		actual := make(map[OwnerID]int, len(actualStrings))
		for k, v := range actualStrings {
			actual[OwnerID(k)], err = strconv.Atoi(v)
			require.NoError(t, err)
		}

		assert.Equal(t, expected, actual)
	}

	assertLeases := func(expected map[TaskID]OwnerID) {
		actual, err := valkey.StringMap(vc.Do("HGETALL", "{test}:leases"))
		require.NoError(t, err)

		expectedStrs := make(map[string]string, len(expected))
		for id, owner := range expected {
			expectedStrs[string(id)] = string(owner)
		}
		assert.Equal(t, expectedStrs, actual)

		// and every lease has an expiry
		assertvk.ZCard(t, vc, "{test}:expires", len(expected))
	}

	assertTasks := func(owner OwnerID, expected0, expected1 []string) {
		actual0, err := valkey.Strings(vc.Do("LRANGE", "{test}:o:"+owner+"/0", 0, -1))
		require.NoError(t, err)
		actual1, err := valkey.Strings(vc.Do("LRANGE", "{test}:o:"+owner+"/1", 0, -1))
		require.NoError(t, err)

		assert.Equal(t, expected0, actual0, "priority 0 tasks mismatch")
		assert.Equal(t, expected1, actual1, "priority 1 tasks mismatch")

		// checked .Size() method as well
		size, err := q.size(ctx, vc, owner)
		assert.NoError(t, err)
		assert.Equal(t, len(expected0)+len(expected1), size)
	}

	assertDump := func(expected string) {
		dump, err := q.dump(ctx, vc)
		require.NoError(t, err)
		assert.JSONEq(t, expected, string(dump), "dumped queue state does not match expected")
	}

	assertQueued(map[OwnerID]int{})
	assertActive(map[OwnerID]int{})
	assertTasks("owner1", []string{}, []string{})
	assertTasks("owner2", []string{}, []string{})
	assertDump(`{"queued": {}, "active": {}, "paused": {}, "leased": {}}`)

	task1UUID := assertPush(t, q, vc, "owner1", false, []byte(`task1`))
	task2UUID := assertPush(t, q, vc, "owner1", true, []byte(`task2`))
	task3UUID := assertPush(t, q, vc, "owner2", false, []byte(`task3`))
	task4UUID := assertPush(t, q, vc, "owner1", false, []byte(`task4`))
	task5UUID := assertPush(t, q, vc, "owner2", true, []byte(`task5`))

	// nobody processing any tasks so no workers assigned in active set
	assertQueued(map[OwnerID]int{"owner1": 3, "owner2": 2})
	assertActive(map[OwnerID]int{})
	assertTasks("owner1", []string{"01980000-0000-7000-8000-000000000001|task1", "01980000-0000-7000-8000-000000000004|task4"}, []string{"01980000-0000-7000-8000-000000000002|task2"})
	assertTasks("owner2", []string{"01980000-0000-7000-8000-000000000003|task3"}, []string{"01980000-0000-7000-8000-000000000005|task5"})

	assertPop(t, q, vc, task2UUID, "owner1", "task2") // because it's highest priority for owner 1
	assertQueued(map[OwnerID]int{"owner1": 2, "owner2": 2})
	assertActive(map[OwnerID]int{"owner1": 1})

	assertPop(t, q, vc, task5UUID, "owner2", "task5") // because it's highest priority for owner 2
	assertQueued(map[OwnerID]int{"owner1": 2, "owner2": 1})
	assertActive(map[OwnerID]int{"owner1": 1, "owner2": 1})

	assertPop(t, q, vc, task1UUID, "owner1", "task1")
	assertQueued(map[OwnerID]int{"owner1": 1, "owner2": 1})
	assertActive(map[OwnerID]int{"owner1": 2, "owner2": 1})
	assertTasks("owner1", []string{"01980000-0000-7000-8000-000000000004|task4"}, []string{})
	assertTasks("owner2", []string{"01980000-0000-7000-8000-000000000003|task3"}, []string{})
	assertLeases(map[TaskID]OwnerID{task2UUID: "owner1", task5UUID: "owner2", task1UUID: "owner1"})
	assertDump(`{"queued": {"owner1": 1, "owner2": 1}, "active": {"owner1": 2, "owner2": 1}, "paused": {}, "leased": {"owner1": 2, "owner2": 1}}`)

	// mark task2 and task1 (owner1) as complete
	assertDone(t, q, vc, task2UUID, true)
	assertDone(t, q, vc, task1UUID, true)
	assertLeases(map[TaskID]OwnerID{task5UUID: "owner2"})

	assertQueued(map[OwnerID]int{"owner1": 1, "owner2": 1})
	assertActive(map[OwnerID]int{"owner2": 1})

	assertPop(t, q, vc, task4UUID, "owner1", "task4")
	assertPop(t, q, vc, task3UUID, "owner2", "task3")
	assertTasks("owner1", []string{}, []string{})
	assertTasks("owner2", []string{}, []string{})

	assertQueued(map[OwnerID]int{})
	assertActive(map[OwnerID]int{"owner1": 1, "owner2": 2})

	assertPop(t, q, vc, "", "", "") // no more tasks
	assertTasks("owner1", []string{}, []string{})
	assertTasks("owner2", []string{}, []string{})

	assertQueued(map[OwnerID]int{})
	assertActive(map[OwnerID]int{"owner1": 1, "owner2": 2})

	// mark remaining tasks as complete
	assertDone(t, q, vc, task4UUID, true)
	assertDone(t, q, vc, task3UUID, true)
	assertDone(t, q, vc, task5UUID, true)
	assertLeases(map[TaskID]OwnerID{})

	assertQueued(map[OwnerID]int{})
	assertActive(map[OwnerID]int{})

	task6UUID := assertPush(t, q, vc, "owner1", false, []byte(`task6`))
	task7UUID := assertPush(t, q, vc, "owner1", false, []byte(`task7`))
	task8UUID := assertPush(t, q, vc, "owner2", false, []byte(`task8`))
	task9UUID := assertPush(t, q, vc, "owner2", false, []byte(`task9`))

	assertPop(t, q, vc, task6UUID, "owner1", "task6")

	q.pause(ctx, vc, "owner1")
	q.pause(ctx, vc, "owner1") // no-op if already paused

	assertQueued(map[OwnerID]int{"owner1": 1, "owner2": 2})
	assertActive(map[OwnerID]int{"owner1": 1})
	assertDump(`{"queued": {"owner1": 1, "owner2": 2}, "active": {"owner1": 1}, "paused": {"owner1": 1}, "leased": {"owner1": 1}}`)

	paused, err := q.paused(ctx, vc)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []OwnerID{"owner1"}, paused)

	assertPop(t, q, vc, task8UUID, "owner2", "task8")
	assertPop(t, q, vc, task9UUID, "owner2", "task9")
	assertPop(t, q, vc, "", "", "") // no more tasks

	q.resume(ctx, vc, "owner1")
	q.resume(ctx, vc, "owner1") // no-op if already active

	assertQueued(map[OwnerID]int{"owner1": 1})
	assertActive(map[OwnerID]int{"owner1": 1, "owner2": 2})

	paused, err = q.paused(ctx, vc)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{}, paused)

	assertPop(t, q, vc, task7UUID, "owner1", "task7")

	assertDone(t, q, vc, task6UUID, true)
	assertDone(t, q, vc, task7UUID, true)
	assertDone(t, q, vc, task8UUID, true)
	assertDone(t, q, vc, task9UUID, true)

	assertQueued(map[OwnerID]int{})
	assertActive(map[OwnerID]int{})

	// if we somehow get into a state where an owner is in the queued set but doesn't have queued tasks, pop will retry
	assertPush(t, q, vc, "owner1", false, []byte("task10"))
	task11UUID := assertPush(t, q, vc, "owner2", false, []byte("task11"))

	assertQueued(map[OwnerID]int{"owner1": 1, "owner2": 1})
	assertActive(map[OwnerID]int{})

	assertvk.LLen(t, vc, "{test}:o:owner1/0", 1)
	_, err = vc.Do("DEL", "{test}:o:owner1/0") // task10 gone
	assert.NoError(t, err)

	assertPop(t, q, vc, task11UUID, "owner2", "task11")
	assertPop(t, q, vc, "", "", "")

	assertQueued(map[OwnerID]int{})
	assertActive(map[OwnerID]int{"owner2": 1})

	// marking a task done more than once is a no-op
	assertDone(t, q, vc, task11UUID, true)
	assertDone(t, q, vc, task11UUID, false)

	assertActive(map[OwnerID]int{})
	assertLeases(map[TaskID]OwnerID{})
}

func TestFairTaskPayloads(t *testing.T) {
	vp := assertvk.ClaimDB(t).Pool()
	vc := vp.Get()
	defer vc.Close()

	q := newFair("test", 2, time.Minute)

	task1UUID := assertPush(t, q, vc, "owner1", true, []byte(`{"foo": "|"}`))
	task2UUID := assertPush(t, q, vc, "owner1", true, []byte(`task2`))

	assertPop(t, q, vc, task1UUID, "owner1", `{"foo": "|"}`)
	assertPop(t, q, vc, task2UUID, "owner1", "task2")
}

func TestFairMaxActivePerOwner(t *testing.T) {
	vp := assertvk.ClaimDB(t).Pool()
	vc := vp.Get()
	defer vc.Close()

	q := newFair("test", 2, time.Minute)

	task1UUID := assertPush(t, q, vc, "owner1", false, []byte(`task1`))
	task2UUID := assertPush(t, q, vc, "owner1", true, []byte(`task2`))
	task3UUID := assertPush(t, q, vc, "owner1", false, []byte(`task3`))

	assertPop(t, q, vc, task2UUID, "owner1", "task2")
	assertPop(t, q, vc, task1UUID, "owner1", "task1")
	assertPop(t, q, vc, "", "", "") // owner1 has reached max active tasks

	assertDone(t, q, vc, task2UUID, true)

	assertPop(t, q, vc, task3UUID, "owner1", "task3") // now we can pop task3
}

func TestFairLeaseExpiry(t *testing.T) {
	ctx := t.Context()
	vp := assertvk.ClaimDB(t).Pool()
	vc := vp.Get()
	defer vc.Close()

	q := newFair("test", 1, 200*time.Millisecond)

	task1UUID := assertPush(t, q, vc, "owner1", false, []byte(`task1`))
	task2UUID := assertPush(t, q, vc, "owner1", false, []byte(`task2`))
	task3UUID := assertPush(t, q, vc, "owner1", false, []byte(`task3`))

	assertPop(t, q, vc, task1UUID, "owner1", "task1")
	assertPop(t, q, vc, "", "", "") // owner1 has reached max active tasks

	// consumer of task1 dies without marking it done.. once its lease expires, a pop releases its slot
	time.Sleep(250 * time.Millisecond)

	id, owner, task, expired, err := q.pop(ctx, vc)
	require.NoError(t, err)
	assert.Equal(t, task2UUID, id)
	assert.Equal(t, OwnerID("owner1"), owner)
	assert.Equal(t, "task2", string(task))
	assert.Equal(t, []expiredLease{{ID: task1UUID, Owner: "owner1"}}, expired)

	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{"owner1": 1})
	assertvk.HGetAll(t, vc, "{test}:leases", map[string]string{string(task2UUID): "owner1"})

	// the consumer of task1 was actually just slow.. marking it done now is a no-op
	assertDone(t, q, vc, task1UUID, false)
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{"owner1": 1})

	// a lease expiring doesn't require there to be a task to pop
	assertDone(t, q, vc, task2UUID, true)
	assertPop(t, q, vc, task3UUID, "owner1", "task3")
	time.Sleep(250 * time.Millisecond)

	id, _, _, expired, err = q.pop(ctx, vc)
	require.NoError(t, err)
	assert.Equal(t, TaskID(""), id)
	assert.Equal(t, []expiredLease{{ID: task3UUID, Owner: "owner1"}}, expired)

	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{})
	assertvk.HLen(t, vc, "{test}:leases", 0)
	assertvk.ZCard(t, vc, "{test}:expires", 0)
}

func TestFairConcurrency(t *testing.T) {
	ctx := t.Context()
	vp := assertvk.ClaimDB(t).Pool()
	vc := vp.Get()
	defer vc.Close()

	q := newFair("test", 5, time.Minute) // one owner can only occupy 5 of the 10 consumers at a time

	type ownerAndTask struct {
		owner OwnerID
		task  string
	}

	numTasks := 10000
	pushedTasks := make([]*ownerAndTask, 0, numTasks)
	poppedTasks := make([]*ownerAndTask, 0, numTasks)

	var wg sync.WaitGroup
	var mutex sync.Mutex

	recordTaskPushed := func(owner OwnerID, task string) {
		mutex.Lock()
		defer mutex.Unlock()

		pushedTasks = append(pushedTasks, &ownerAndTask{owner: owner, task: task})
	}

	recordTaskProcessed := func(owner OwnerID, task string) {
		mutex.Lock()
		defer mutex.Unlock()

		poppedTasks = append(poppedTasks, &ownerAndTask{owner: owner, task: task})
	}

	// Start 5 producers to push tasks each concurrently
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			vc := vp.Get()
			defer vc.Close()

			for range numTasks / 5 {
				owner := OwnerID(fmt.Sprintf("owner%d", rand.IntN(5)+1)) // five possible owners (1...5)
				task := []byte(string(uuids.NewV7()))
				_, err := q.push(ctx, vc, owner, false, task)
				assert.NoError(t, err, "Producer %d failed to push task for owner %s", i, owner)

				recordTaskPushed(owner, string(task))

				time.Sleep(time.Duration(rand.IntN(5)) * time.Millisecond)
			}
		}()
	}

	// Start 10 consumers to pop tasks concurrently
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			vc := vp.Get()
			defer vc.Close()

			for {
				id, owner, task, _, err := q.pop(ctx, vc)
				assert.NoError(t, err, "Consumer %d failed to pop task", i)

				if id != "" {
					time.Sleep(time.Duration(rand.IntN(5)) * time.Millisecond)

					released, err := q.done(ctx, vc, id)
					assert.NoError(t, err, "Consumer %d failed to mark task done", i)
					assert.True(t, released, "Consumer %d found task %s already released", i, id)

					recordTaskProcessed(owner, string(task))
				}
				// Check if all tasks have been processed
				mutex.Lock()
				allDone := len(poppedTasks) >= numTasks
				mutex.Unlock()

				if allDone {
					return
				} else {
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	wg.Wait() // Wait for all producers and consumers to complete

	// can't guarantee order of processed tasks, but we can check that all expected tasks were processed
	assert.ElementsMatch(t, pushedTasks, poppedTasks)

	assertvk.ZGetAll(t, vc, "{test}:queued", map[string]float64{})
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{})
	assertvk.HLen(t, vc, "{test}:leases", 0)
	assertvk.ZCard(t, vc, "{test}:expires", 0)

	for i := range 5 {
		assertvk.LGetAll(t, vc, fmt.Sprintf("{test}:o:owner%d/0", i+1), []string{})
		assertvk.LGetAll(t, vc, fmt.Sprintf("{test}:o:owner%d/1", i+1), []string{})
	}
}

func TestFairConcurrencyWithDeaths(t *testing.T) {
	ctx := t.Context()
	vp := assertvk.ClaimDB(t).Pool()
	vc := vp.Get()
	defer vc.Close()

	// short lease (real time) so slots of tasks abandoned by dead consumers are released quickly
	q := newFair("test", 3, 300*time.Millisecond)

	numTasks := 300
	for range numTasks {
		owner := OwnerID(fmt.Sprintf("owner%d", rand.IntN(3)+1))
		_, err := q.push(ctx, vc, owner, false, []byte(string(uuids.NewV7())))
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	var mutex sync.Mutex
	popped, abandoned := 0, 0

	// start 10 consumers which "die" on ~20% of tasks, i.e. never mark them done
	for i := range 10 {
		wg.Go(func() {
			vc := vp.Get()
			defer vc.Close()

			for {
				id, _, _, _, err := q.pop(ctx, vc)
				assert.NoError(t, err, "Consumer %d failed to pop task", i)

				mutex.Lock()
				if id != "" {
					popped++
				}
				allPopped := popped >= numTasks
				mutex.Unlock()

				if id != "" {
					if rand.IntN(5) == 0 {
						mutex.Lock()
						abandoned++
						mutex.Unlock()
					} else {
						_, err := q.done(ctx, vc, id)
						assert.NoError(t, err, "Consumer %d failed to mark task done", i)
					}
				}

				if allPopped {
					return
				}
				time.Sleep(time.Millisecond)
			}
		})
	}

	wg.Wait()

	assert.Equal(t, numTasks, popped)
	assert.Greater(t, abandoned, 0)

	// once remaining leases expire, the next pop releases every slot still held by abandoned tasks
	time.Sleep(350 * time.Millisecond)

	for range numTasks / reapLimit {
		id, _, _, _, err := q.pop(ctx, vc)
		require.NoError(t, err)
		assert.Equal(t, TaskID(""), id)
	}

	assertvk.ZGetAll(t, vc, "{test}:queued", map[string]float64{})
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{})
	assertvk.HLen(t, vc, "{test}:leases", 0)
	assertvk.ZCard(t, vc, "{test}:expires", 0)
}

// assertPush is a helper function that asserts the result of a Push operation
func assertPush(t *testing.T, q *fair, vc valkey.Conn, owner OwnerID, priority bool, task []byte) TaskID {
	ctx := t.Context()

	id, err := q.push(ctx, vc, owner, priority, task)
	assert.NoError(t, err)
	return id
}

// assertPop is a helper function that asserts the result of a pop operation
func assertPop(t *testing.T, q *fair, vc valkey.Conn, expectedID TaskID, expectedOwner OwnerID, expectedTask string) {
	id, owner, task, _, err := q.pop(t.Context(), vc)
	require.NoError(t, err)
	if expectedTask != "" {
		assert.Equal(t, expectedID, id)
		assert.Equal(t, expectedOwner, owner)
		assert.Equal(t, expectedTask, string(task))
	} else {
		assert.Equal(t, TaskID(""), id)
		assert.Nil(t, task)
	}
}

// assertDone is a helper function that asserts the result of a done operation
func assertDone(t *testing.T, q *fair, vc valkey.Conn, id TaskID, expectedReleased bool) {
	released, err := q.done(t.Context(), vc, id)
	require.NoError(t, err)
	assert.Equal(t, expectedReleased, released, "released mismatch for task %s", id)
}
