package testsuite

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func QueueBatchTask(t *testing.T, rt *runtime.Runtime, org *testdb.Org, task tasks.Task) {
	rc := rt.RP.Get()
	defer rc.Close()

	err := tasks.Queue(rc, tasks.BatchQueue, org.ID, task, false)
	require.NoError(t, err)
}

func QueueContactTask(t *testing.T, rt *runtime.Runtime, org *testdb.Org, contact *testdb.Contact, ctask handler.Task) {
	rc := rt.RP.Get()
	defer rc.Close()

	err := handler.QueueTask(rc, org.ID, contact.ID, ctask)
	require.NoError(t, err)
}

func CurrentTasks(t *testing.T, rt *runtime.Runtime, qname string) map[models.OrgID][]*queues.Task {
	rc := rt.RP.Get()
	defer rc.Close()

	// get all active org queues
	active, err := redis.Ints(rc.Do("ZRANGE", fmt.Sprintf("tasks:%s:active", qname), 0, -1))
	require.NoError(t, err)

	tasks := make(map[models.OrgID][]*queues.Task)
	for _, orgID := range active {
		orgTasksEncoded, err := redis.Strings(rc.Do("ZRANGE", fmt.Sprintf("tasks:%s:%d", qname, orgID), 0, -1))
		require.NoError(t, err)

		orgTasks := make([]*queues.Task, len(orgTasksEncoded))

		for i := range orgTasksEncoded {
			task := &queues.Task{}
			jsonx.MustUnmarshal([]byte(orgTasksEncoded[i]), task)
			orgTasks[i] = task
		}

		tasks[models.OrgID(orgID)] = orgTasks
	}

	return tasks
}

func FlushTasks(t *testing.T, rt *runtime.Runtime, qnames ...string) map[string]int {
	rc := rt.RP.Get()
	defer rc.Close()

	var task *queues.Task
	var err error
	counts := make(map[string]int)

	var qs []queues.Fair
	for _, q := range []queues.Fair{tasks.HandlerQueue, tasks.BatchQueue, tasks.ThrottledQueue} {
		if len(qnames) == 0 || slices.Contains(qnames, fmt.Sprint(q)[6:]) {
			qs = append(qs, q)
		}
	}

	for {
		// look for a task in the queues
		for _, q := range qs {
			task, err = q.Pop(rc)
			require.NoError(t, err)

			if task != nil {
				break
			}
		}

		if task == nil { // all done
			break
		}

		counts[task.Type]++

		err = tasks.Perform(context.Background(), rt, task)
		assert.NoError(t, err)
	}
	return counts
}
