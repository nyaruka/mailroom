package queues

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/vkutil/queues"
)

type FairV2 struct {
	name string
	base queues.Fair
}

func NewFairV2(keyBase string, maxActivePerOwner int) *FairV2 {
	return &FairV2{
		name: keyBase,
		base: *queues.NewFair(keyBase, maxActivePerOwner),
	}
}

func (q *FairV2) String() string {
	return q.name
}

func (q *FairV2) Push(ctx context.Context, rc redis.Conn, taskType string, ownerID int, task any, priority bool) error {
	taskJSON := jsonx.MustMarshal(task)

	wrapper := &Task{Type: taskType, OwnerID: ownerID, Task: taskJSON, QueuedOn: dates.Now()}
	raw := jsonx.MustMarshal(wrapper)

	return q.base.Push(ctx, rc, fmt.Sprint(ownerID), priority, raw)
}

func (q *FairV2) Owners(ctx context.Context, rc redis.Conn) ([]int, error) {
	strs, err := q.base.Queued(ctx, rc)
	if err != nil {
		return nil, err
	}

	actual := make([]int, len(strs))
	for i, s := range strs {
		owner, _ := strconv.ParseInt(s, 10, 64)
		actual[i] = int(owner)
	}

	return actual, nil
}

func (q *FairV2) Pop(ctx context.Context, rc redis.Conn) (*Task, error) {
	owner, raw, err := q.base.Pop(ctx, rc)
	if err != nil {
		return nil, fmt.Errorf("error popping task: %w", err)
	}

	task := &Task{}
	if err := jsonx.Unmarshal(raw, task); err != nil {
		return nil, fmt.Errorf("error unmarshaling task: %w", err)
	}

	task.OwnerID, _ = strconv.Atoi(owner)

	return task, nil
}

func (q *FairV2) Done(ctx context.Context, rc redis.Conn, ownerID int) error {
	return q.base.Done(ctx, rc, fmt.Sprint(ownerID))
}

func (q *FairV2) Size(ctx context.Context, rc redis.Conn) (int, error) {
	owners, err := q.base.Queued(ctx, rc)
	if err != nil {
		return 0, fmt.Errorf("error getting queued task owners: %w", err)
	}

	total := 0
	for _, owner := range owners {
		size, err := q.base.Size(ctx, rc, owner)
		if err != nil {
			return 0, fmt.Errorf("error getting size for owner %s: %w", owner, err)
		}
		total += size
	}

	return total, nil
}

func (q *FairV2) Pause(ctx context.Context, rc redis.Conn, ownerID int) error {
	return q.base.Pause(ctx, rc, fmt.Sprint(ownerID))
}

func (q *FairV2) Resume(ctx context.Context, rc redis.Conn, ownerID int) error {
	return q.base.Resume(ctx, rc, fmt.Sprint(ownerID))
}

var _ Fair = (*FairV2)(nil)
