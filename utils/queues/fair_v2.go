package queues

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
)

type FairV2 struct {
	name string
	base *fair
}

func NewFair(name string, maxActivePerOwner int, lease time.Duration) *FairV2 {
	return &FairV2{
		name: name,
		base: newFair(fmt.Sprintf("tasks:%s", name), maxActivePerOwner, lease),
	}
}

func (q *FairV2) String() string {
	return q.name
}

func (q *FairV2) Push(ctx context.Context, vc valkey.Conn, taskType string, ownerID int, task any, priority bool) (TaskID, error) {
	taskJSON := jsonx.MustMarshal(task)

	wrapper := &Task{Type: taskType, OwnerID: ownerID, Task: taskJSON, QueuedOn: dates.Now()}
	raw := jsonx.MustMarshal(wrapper)

	return q.base.push(ctx, vc, OwnerID(fmt.Sprint(ownerID)), priority, raw)
}

func (q *FairV2) Pop(ctx context.Context, vc valkey.Conn) (*Task, error) {
	taskID, ownerID, raw, expired, err := q.base.pop(ctx, vc)

	for _, e := range expired {
		slog.Warn("task lease expired, releasing its slot", "queue", q.name, "task_id", e.ID, "org", e.Owner)
	}

	if err != nil {
		return nil, fmt.Errorf("error popping task: %w", err)
	}
	if taskID == "" {
		return nil, nil // no task available
	}

	task := &Task{}
	if err := jsonx.Unmarshal(raw, task); err != nil {
		q.base.done(ctx, vc, taskID) // release its slot now rather than when its lease expires
		return nil, fmt.Errorf("error unmarshaling task %s: %w", taskID, err)
	}

	task.ID = taskID
	task.OwnerID, _ = strconv.Atoi(string(ownerID))

	return task, nil
}

func (q *FairV2) Done(ctx context.Context, vc valkey.Conn, task *Task) error {
	released, err := q.base.done(ctx, vc, task.ID)
	if err != nil {
		return err
	}
	if !released {
		slog.Warn("task completed after its lease expired", "queue", q.name, "task_id", task.ID, "org", task.OwnerID, "type", task.Type)
	}
	return nil
}

func (q *FairV2) Queued(ctx context.Context, vc valkey.Conn) ([]int, error) {
	strs, err := q.base.queued(ctx, vc)
	if err != nil {
		return nil, err
	}

	actual := make([]int, len(strs))
	for i, s := range strs {
		owner, _ := strconv.ParseInt(string(s), 10, 64)
		actual[i] = int(owner)
	}

	return actual, nil
}

func (q *FairV2) Paused(ctx context.Context, vc valkey.Conn) ([]int, error) {
	strs, err := q.base.paused(ctx, vc)
	if err != nil {
		return nil, err
	}

	actual := make([]int, len(strs))
	for i, s := range strs {
		owner, _ := strconv.ParseInt(string(s), 10, 64)
		actual[i] = int(owner)
	}

	return actual, nil
}

func (q *FairV2) Size(ctx context.Context, vc valkey.Conn) (int, error) {
	owners, err := q.base.queued(ctx, vc)
	if err != nil {
		return 0, fmt.Errorf("error getting queued task owners: %w", err)
	}

	total := 0
	for _, owner := range owners {
		size, err := q.base.size(ctx, vc, owner)
		if err != nil {
			return 0, fmt.Errorf("error getting size for owner %s: %w", owner, err)
		}
		total += size
	}

	return total, nil
}

func (q *FairV2) Pause(ctx context.Context, vc valkey.Conn, ownerID int) error {
	return q.base.pause(ctx, vc, OwnerID(fmt.Sprint(ownerID)))
}

func (q *FairV2) Resume(ctx context.Context, vc valkey.Conn, ownerID int) error {
	return q.base.resume(ctx, vc, OwnerID(fmt.Sprint(ownerID)))
}

func (q *FairV2) Dump(ctx context.Context, vc valkey.Conn) ([]byte, error) {
	return q.base.dump(ctx, vc)
}

var _ Fair = (*FairV2)(nil)
