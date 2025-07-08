package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/queues"
)

var HandlerQueue = queues.NewFairSorted("tasks:handler")
var BatchQueue = queues.NewFairSorted("tasks:batch")
var ThrottledQueue = queues.NewFairSorted("tasks:throttled")

var registeredTypes = map[string](func() Task){}

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

	start := time.Now()

	err = typedTask.Perform(ctx, rt, oa)

	// log if task took longer than 75% of its timeout
	if duration := time.Since(start); duration >= (3 * typedTask.Timeout() / 4) {
		slog.Error("task took longer than expected", "org", oa.OrgID(), "type", typedTask.Type(), "duration", duration, "limit", typedTask.Timeout())
	}

	return err
}

// Queue adds the given task to the given queue
func Queue(ctx context.Context, rc redis.Conn, q queues.Fair, orgID models.OrgID, task Task, priority bool) error {
	return q.Push(ctx, rc, task.Type(), int(orgID), task, priority)
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
