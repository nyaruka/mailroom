package testsuite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
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

type TaskInfo struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func GetQueuedTasks(t *testing.T, rt *runtime.Runtime) map[string][]TaskInfo {
	t.Helper()

	actual := make(map[string][]TaskInfo)

	for _, qname := range []string{"realtime", "batch", "throttled"} {
		for orgID, oTasks := range CurrentTasks(t, rt, qname) {
			key := fmt.Sprintf("%s/%d", qname, orgID)
			actual[key] = make([]TaskInfo, len(oTasks))
			for i, task := range oTasks {
				actual[key][i] = TaskInfo{Type: task.Type, Payload: task.Task}
			}
		}
	}

	return actual
}

func GetQueuedTaskTypes(t *testing.T, rt *runtime.Runtime) map[string][]string {
	t.Helper()

	actual := make(map[string][]string)

	for key, tasks := range GetQueuedTasks(t, rt) {
		types := make([]string, len(tasks))
		for i, task := range tasks {
			types[i] = task.Type
		}
		actual[key] = types
	}

	return actual
}

// FlushTasks processes any queued tasks
func FlushTasks(t *testing.T, rt *runtime.Runtime, qnames ...string) map[string]int {
	return drainTasks(t, rt, true, qnames...)
}

// ClearTasks removes any queued tasks without processing them
func ClearTasks(t *testing.T, rt *runtime.Runtime, qnames ...string) map[string]int {
	return drainTasks(t, rt, false, qnames...)
}

func drainTasks(t *testing.T, rt *runtime.Runtime, perform bool, qnames ...string) map[string]int {
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
			task, err = q.Pop(t.Context(), vc)
			require.NoError(t, err)

			if task != nil {
				break
			}
		}

		if task == nil { // all done
			break
		}

		counts[task.Type]++

		if perform {
			err = tasks.Perform(t.Context(), rt, task)
			require.NoError(t, err, "unexpected error performing task %s", task.Type)
		}

		err = q.Done(t.Context(), vc, task.OwnerID)
		require.NoError(t, err, "unexpected error marking task %s as done", task.Type)
	}
	return counts
}

func GetHistoryItems(t *testing.T, rt *runtime.Runtime, clear bool) []*models.DynamoItem {
	rt.Writers.History.Flush()

	items := dyntest.ScanAll[models.DynamoItem](t, rt.Dynamo, "TestHistory")

	if clear {
		dyntest.Truncate(t, rt.Dynamo, "TestHistory")
	}

	return items
}

func GetHistoryEventTypes(t *testing.T, rt *runtime.Runtime) map[flows.ContactUUID][]string {
	rt.Writers.History.Flush()

	evtTypes := make(map[flows.ContactUUID][]string, 4)

	for _, evt := range dyntest.ScanAll[models.Event](t, rt.Dynamo, "TestHistory") {
		evtTypes[evt.ContactUUID] = append(evtTypes[evt.ContactUUID], evt.Event.Type())
	}

	return evtTypes
}
