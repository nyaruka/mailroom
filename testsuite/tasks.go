package testsuite

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/require"
)

func QueueBatchTask(t *testing.T, rt *runtime.Runtime, org *testdb.Org, task tasks.Task) {
	ctx := context.Background()

	err := tasks.Queue(ctx, rt, rt.Queues.Batch, org.ID, task, false)
	require.NoError(t, err)
}

func QueueRealtimeTask(t *testing.T, rt *runtime.Runtime, org *testdb.Org, contact *testdb.Contact, ctask realtime.Task) {
	ctx := context.Background()

	err := realtime.QueueTask(ctx, rt, org.ID, contact.ID, ctask)
	require.NoError(t, err)
}

func CurrentTasks(t *testing.T, rt *runtime.Runtime, qname string) map[models.OrgID][]*queues.Task {
	vc := rt.VK.Get()
	defer vc.Close()

	queued, err := redis.Ints(vc.Do("ZRANGE", fmt.Sprintf("{tasks:%s}:queued", qname), 0, -1))
	require.NoError(t, err)

	tasks := make(map[models.OrgID][]*queues.Task)
	for _, orgID := range queued {
		tasks1, err := redis.Strings(vc.Do("LRANGE", fmt.Sprintf("{tasks:%s}:o:%d/1", qname, orgID), 0, -1))
		require.NoError(t, err)

		tasks0, err := redis.Strings(vc.Do("LRANGE", fmt.Sprintf("{tasks:%s}:o:%d/0", qname, orgID), 0, -1))
		require.NoError(t, err)

		orgTasks := make([]*queues.Task, len(tasks1)+len(tasks0))

		for i, rawTask := range slices.Concat(tasks1, tasks0) {
			parts := bytes.SplitN([]byte(rawTask), []byte("|"), 2) // split into id and task json

			task := &queues.Task{}
			jsonx.MustUnmarshal(parts[1], task)
			orgTasks[i] = task
		}

		tasks[models.OrgID(orgID)] = orgTasks
	}

	return tasks
}

func FlushTasks(t *testing.T, rt *runtime.Runtime, qnames ...string) map[string]int {
	ctx := context.Background()

	vc := rt.VK.Get()
	defer vc.Close()

	var task *queues.Task
	var err error
	counts := make(map[string]int)

	var qs []queues.Fair
	for _, q := range []queues.Fair{rt.Queues.Realtime, rt.Queues.Batch, rt.Queues.Throttled} {
		if len(qnames) == 0 || slices.Contains(qnames, fmt.Sprint(q)) {
			qs = append(qs, q)
		}
	}

	for {
		// look for a task in the queues
		var q queues.Fair
		for _, q = range qs {
			task, err = q.Pop(ctx, vc)
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
		require.NoError(t, err, "unexpected error performing task %s", task.Type)

		err = q.Done(ctx, vc, task.OwnerID)
		require.NoError(t, err, "unexpected error marking task %s as done", task.Type)
	}
	return counts
}
