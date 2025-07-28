package queues

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
)

type FairLegacy struct {
	keyBase string
}

func NewFairLegacy(keyBase string) *FairLegacy {
	return &FairLegacy{keyBase: keyBase}
}

func (q *FairLegacy) String() string {
	return q.keyBase
}

// Push adds the passed in task to our queue for execution
func (q *FairLegacy) Push(ctx context.Context, vc redis.Conn, taskType string, ownerID int, task any, priority bool) error {
	score := q.score(priority)

	taskBody, err := json.Marshal(task)
	if err != nil {
		return err
	}

	wrapper := &Task{Type: taskType, OwnerID: ownerID, Task: taskBody, QueuedOn: dates.Now()}
	marshaled := jsonx.MustMarshal(wrapper)

	vc.Send("ZADD", q.queueKey(ownerID), score, marshaled)
	vc.Send("ZINCRBY", q.activeKey(), 0, ownerID) // ensure exists in active set
	_, err = redis.DoContext(vc, ctx, "")
	return err
}

func (q *FairLegacy) Queued(ctx context.Context, vc redis.Conn) ([]int, error) {
	strs, err := redis.Strings(redis.DoContext(vc, ctx, "ZRANGE", q.activeKey(), 0, -1))
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

//go:embed lua/fair_sorted_pop.lua
var luaFSPop string
var scriptFSPop = redis.NewScript(1, luaFSPop)

// Pop pops the next task off our queue
func (q *FairLegacy) Pop(ctx context.Context, vc redis.Conn) (*Task, error) {
	task := &Task{}
	for {
		values, err := redis.Strings(scriptFSPop.DoContext(ctx, vc, q.activeKey(), q.keyBase))
		if err != nil {
			return nil, err
		}

		if values[0] == "empty" {
			return nil, nil
		}

		if values[0] == "retry" {
			continue
		}

		err = json.Unmarshal([]byte(values[1]), task)
		if err != nil {
			return nil, err
		}

		ownerID, err := strconv.Atoi(values[0])
		if err != nil {
			return nil, err
		}

		task.OwnerID = ownerID

		return task, err
	}
}

//go:embed lua/fair_sorted_done.lua
var luaFSDone string
var scriptFSDone = redis.NewScript(1, luaFSDone)

// Done marks the passed in task as complete. Callers must call this in order
// to maintain fair workers across orgs
func (q *FairLegacy) Done(ctx context.Context, vc redis.Conn, ownerID int) error {
	_, err := scriptFSDone.DoContext(ctx, vc, q.activeKey(), strconv.FormatInt(int64(ownerID), 10))
	return err
}

//go:embed lua/fair_sorted_size.lua
var luaFSSize string
var scriptFSSize = redis.NewScript(1, luaFSSize)

// Size returns the total number of tasks for the passed in queue across all owners
func (q *FairLegacy) Size(ctx context.Context, vc redis.Conn) (int, error) {
	return redis.Int(scriptFSSize.DoContext(ctx, vc, q.activeKey(), q.keyBase))
}

//go:embed lua/fair_sorted_pause.lua
var luaFSPause string
var scriptFSPause = redis.NewScript(1, luaFSPause)

// Pause marks the given task owner as paused so their tasks are not popped.
func (q *FairLegacy) Pause(ctx context.Context, vc redis.Conn, ownerID int) error {
	_, err := scriptFSPause.DoContext(ctx, vc, q.activeKey(), strconv.FormatInt(int64(ownerID), 10))
	return err
}

// Paused is not supported for this queue type
func (q *FairLegacy) Paused(ctx context.Context, vc redis.Conn) ([]int, error) {
	panic("unsupported")
}

//go:embed lua/fair_sorted_resume.lua
var luaFSResume string
var scriptFSResume = redis.NewScript(1, luaFSResume)

// Resume marks the given task owner as active so their tasks will be popped.
func (q *FairLegacy) Resume(ctx context.Context, vc redis.Conn, ownerID int) error {
	_, err := scriptFSResume.DoContext(ctx, vc, q.activeKey(), strconv.FormatInt(int64(ownerID), 10))
	return err
}

func (q *FairLegacy) activeKey() string {
	return fmt.Sprintf("%s:active", q.keyBase)
}

func (q *FairLegacy) queueKey(ownerID int) string {
	return fmt.Sprintf("%s:%d", q.keyBase, ownerID)
}

func (q *FairLegacy) score(priority bool) string {
	weight := float64(0)
	if priority {
		weight = -10000000
	}

	s := float64(dates.Now().UnixMicro())/float64(1000000) + weight

	return strconv.FormatFloat(s, 'f', 6, 64)
}

var _ Fair = (*FairLegacy)(nil)
