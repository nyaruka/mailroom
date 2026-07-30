package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/utils/queues"
)

// tasks that take longer than this are logged as errors - set slightly below the maximum stop
// timeout for an ECS task on Fargate (120s) so we catch tasks getting dangerously close to a
// duration where they couldn't stop gracefully during a deployment
const maxNormalDuration = 110 * time.Second

// slowThreshold is the duration after which a task is logged as an error - the lesser of
// maxNormalDuration and 75% of the task's own timeout, so that tasks with short timeouts are
// still reported when they get close to timing out
func slowThreshold(timeout time.Duration) time.Duration {
	return min(maxNormalDuration, 3*timeout/4)
}

var registeredTypes = map[string](func() Task){}

// RegisterType registers a new type of task
func RegisterType(name string, initFunc func() Task) {
	registeredTypes[name] = initFunc
}

// TaskID is the unique ID assigned to a task when it's queued
type TaskID = queues.TaskID

// BatchTask is embedded by tasks which are one batch of work split out from an owning task or object. BatchOwnerUUID
// identifies that owner: the UUID of the parent object where there is one (flow start, broadcast), and otherwise the
// ID of the creating task (group population) or a generated UUID (contact imports). TotalBatches is the number of
// batches in the set, carried by every batch so completion tracking needs no separate initialization.
type BatchTask struct {
	BatchOwnerUUID uuids.UUID `json:"batch_owner_uuid,omitempty"`
	TotalBatches   int        `json:"total_batches,omitempty"`
}

// RecordStarted marks processing of this batch's set as started in its owner's tracker and returns whether this was
// the first batch of the set to start. Tracker errors are logged rather than escalated - the cost is just that the
// owner is never marked as started.
func (b *BatchTask) RecordStarted(ctx context.Context, rt *runtime.Runtime) bool {
	if b.BatchOwnerUUID == "" {
		return false // shouldn't happen but better than all such batches sharing a tracker
	}

	first, err := NewBatchTracker(b.BatchOwnerUUID).Started(ctx, rt.VK)
	if err != nil {
		slog.Error("error recording batch task start", "error", err, "owner_uuid", b.BatchOwnerUUID)
		return false
	}

	return first
}

// RecordComplete marks this batch as complete in its owner's tracker and returns whether it was the last batch of its
// set to complete. Tracker errors are logged rather than escalated since the batch's own work has already succeeded -
// tho in that case completion of the set will never be detected.
func (b *BatchTask) RecordComplete(ctx context.Context, rt *runtime.Runtime, taskID TaskID) bool {
	if b.BatchOwnerUUID == "" {
		return false // shouldn't happen but better than all such batches sharing a tracker
	}

	completed, err := NewBatchTracker(b.BatchOwnerUUID).Done(ctx, rt.VK, taskID)
	if err != nil {
		slog.Error("error recording batch task completion", "error", err, "owner_uuid", b.BatchOwnerUUID)
		return false
	}

	return b.TotalBatches > 0 && completed >= b.TotalBatches
}

// Task is the common interface for all task types
type Task interface {
	Type() string

	// Timeout is the maximum amount of time the task can run for
	Timeout() time.Duration

	WithAssets() models.Refresh

	// Perform performs the task, and is passed the unique ID the task was queued with
	Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error
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

	err = typedTask.Perform(ctx, rt, oa, task.ID)

	if duration := time.Since(start); duration >= slowThreshold(typedTask.Timeout()) {
		slog.Error("task took longer than expected", "org", oa.OrgID(), "type", typedTask.Type(), "duration", duration, "timeout", typedTask.Timeout())
	}

	return err
}

// Queue adds the given task to the given queue
func Queue(ctx context.Context, rt *runtime.Runtime, q queues.Fair, orgID models.OrgID, task Task, priority bool) error {
	vc := rt.VK.Get()
	defer vc.Close()

	_, err := q.Push(ctx, vc, task.Type(), int(orgID), task, priority)
	return err
}

//------------------------------------------------------------------------------------------
// JSON Encoding / Decoding
//------------------------------------------------------------------------------------------

// ReadTask reads an action from the given JSON
func ReadTask(typeName string, data []byte) (Task, error) {
	f := registeredTypes[typeName]
	if f == nil {
		return nil, fmt.Errorf("unknown task type: '%s'", typeName)
	}

	task := f()
	return task, utils.UnmarshalAndValidate(data, task)
}
