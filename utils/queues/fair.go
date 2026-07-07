package queues

import (
	"context"
	"fmt"
	"strconv"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/queues"
)

// Fair is a queue that supports fair distribution of tasks between owners
type Fair struct {
	name string
	base *queues.Fair
}

func NewFair(name string, maxActivePerOwner int, lease time.Duration, maxAttempts int) *Fair {
	return &Fair{
		name: name,
		base: queues.NewFair(fmt.Sprintf("tasks:%s", name), maxActivePerOwner, lease, maxAttempts),
	}
}

func (q *Fair) String() string {
	return q.name
}

func (q *Fair) Push(ctx context.Context, vc valkey.Conn, taskType string, ownerID int, task any, priority bool) (queues.TaskID, error) {
	taskJSON := jsonx.MustMarshal(task)

	wrapper := &Task{Type: taskType, OwnerID: ownerID, Task: taskJSON, QueuedOn: dates.Now()}
	raw := jsonx.MustMarshal(wrapper)

	return q.base.Push(ctx, vc, queues.OwnerID(fmt.Sprint(ownerID)), priority, raw)
}

func (q *Fair) Pop(ctx context.Context, vc valkey.Conn) (*Task, error) {
	popped, err := q.base.Pop(ctx, vc)
	if err != nil {
		return nil, fmt.Errorf("error popping task: %w", err)
	}

	if popped == nil {
		return nil, nil // no task available
	}

	task := &Task{}
	if err := jsonx.Unmarshal(popped.Task, task); err != nil {
		return nil, fmt.Errorf("error unmarshaling task %s: %w", popped.ID, err)
	}

	task.ID = popped.ID
	task.OwnerID, _ = strconv.Atoi(string(popped.Owner))
	task.Attempts = popped.Attempts

	return task, nil
}

func (q *Fair) Done(ctx context.Context, vc valkey.Conn, id queues.TaskID) error {
	return q.base.Done(ctx, vc, id)
}

func (q *Fair) Reconcile(ctx context.Context, vc valkey.Conn) error {
	return q.base.Reconcile(ctx, vc)
}

func (q *Fair) Queued(ctx context.Context, vc valkey.Conn) ([]int, error) {
	strs, err := q.base.Queued(ctx, vc)
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

func (q *Fair) Paused(ctx context.Context, vc valkey.Conn) ([]int, error) {
	strs, err := q.base.Paused(ctx, vc)
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

func (q *Fair) Size(ctx context.Context, vc valkey.Conn) (int, error) {
	owners, err := q.base.Queued(ctx, vc)
	if err != nil {
		return 0, fmt.Errorf("error getting queued task owners: %w", err)
	}

	total := 0
	for _, owner := range owners {
		size, err := q.base.Size(ctx, vc, owner)
		if err != nil {
			return 0, fmt.Errorf("error getting size for owner %s: %w", owner, err)
		}
		total += size
	}

	return total, nil
}

func (q *Fair) Pause(ctx context.Context, vc valkey.Conn, ownerID int) error {
	return q.base.Pause(ctx, vc, queues.OwnerID(fmt.Sprint(ownerID)))
}

func (q *Fair) Resume(ctx context.Context, vc valkey.Conn, ownerID int) error {
	return q.base.Resume(ctx, vc, queues.OwnerID(fmt.Sprint(ownerID)))
}

func (q *Fair) Dump(ctx context.Context, vc valkey.Conn) ([]byte, error) {
	return q.base.Dump(ctx, vc)
}
