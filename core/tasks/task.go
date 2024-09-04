package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/queues"
)

var HandlerQueue = queues.NewFairSorted("handler")
var BatchQueue = queues.NewFairSorted("batch")

var registeredTypes = map[string](func() Task){}

var HighLoadThreshold = 1_000_000

// RegisterType registers a new type of task
func RegisterType(name string, initFunc func() Task) {
	registeredTypes[name] = initFunc
}

// Task is the common interface for all task types
type Task interface {
	Type() string

	// Timeout is the maximum amount of time the task can run for
	Timeout() time.Duration

	WithAssets() models.Refresh

	// Perform performs the task
	Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error
}

// Performs a raw task popped from a queue
func Perform(ctx context.Context, rt *runtime.Runtime, task *queues.Task) error {
	// decode our task body
	typedTask, err := ReadTask(task.Type, task.Task)
	if err != nil {
		return fmt.Errorf("error reading task of type %s: %w", task.Type, err)
	}

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, models.OrgID(task.OwnerID), typedTask.WithAssets())
	if err != nil {
		return fmt.Errorf("error loading org assets: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, typedTask.Timeout())
	defer cancel()

	if pauseUnderHighLoad(ctx, rt, task) {
		rc := rt.RP.Get()
		defer rc.Close()

		Queue(rc, BatchQueue, oa.OrgID(), typedTask, queues.DefaultPriority)
		return fmt.Errorf("high load for org: %d, requeued task of type %s", task.OwnerID, task.Type)
	}

	return typedTask.Perform(ctx, rt, oa)
}

// Queue adds the given task to the given queue
func Queue(rc redis.Conn, q *queues.FairSorted, orgID models.OrgID, task Task, priority queues.Priority) error {
	return q.Push(rc, task.Type(), int(orgID), task, priority)
}

// pauseUnderHighLoad to check whether the workspace has a high load at the moment to slow down start flows
func pauseUnderHighLoad(ctx context.Context, rt *runtime.Runtime, task *queues.Task) bool {
	rc := rt.RP.Get()
	defer rc.Close()

	orgLoadCount, err := redis.Int(rc.Do("GET", fmt.Sprintf("high_load:%d", task.OwnerID)))
	if err != nil || orgLoadCount == 0 {

		count := 0
		for _, q := range []string{"batch", "handler"} {
			size, err := redis.Int(rc.Do("ZCARD", fmt.Sprintf("%s:%d", q, task.OwnerID)))
			if err == nil {
				count += size
			}
		}
		if count > HighLoadThreshold {
			// expire in 10min
			rc.Do("SET", fmt.Sprintf("high_load:%d", task.OwnerID), count, "EX", 600)
			orgLoadCount = count
		}
	}

	// Only 5% of tasks will be allowed to be handled under high load
	if (orgLoadCount > HighLoadThreshold) && (task.Type == "start_flow_batch" || task.Type == "start_flow") && (rand.Intn(100) > 5) {
		return true
	}

	return false
}

//------------------------------------------------------------------------------------------
// JSON Encoding / Decoding
//------------------------------------------------------------------------------------------

// ReadTask reads an action from the given JSON
func ReadTask(typeName string, data json.RawMessage) (Task, error) {
	f := registeredTypes[typeName]
	if f == nil {
		return nil, fmt.Errorf("unknown task type: '%s'", typeName)
	}

	task := f()
	return task, utils.UnmarshalAndValidate(data, task)
}
