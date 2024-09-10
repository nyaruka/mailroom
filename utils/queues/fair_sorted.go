package queues

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
)

// Task is a wrapper for encoding a task
type Task struct {
	Type       string          `json:"type"`
	OwnerID    int             `json:"-"`
	Task       json.RawMessage `json:"task"`
	QueuedOn   time.Time       `json:"queued_on"`
	ErrorCount int             `json:"error_count,omitempty"`
}

// Priority is the priority for the task
type Priority int

const (
	// HighPriority is the highest priority for tasks
	HighPriority = Priority(-10000000)

	// DefaultPriority is the default priority for tasks
	DefaultPriority = Priority(0)

	// LowPriority is the lowest priority for tasks
	LowPriority = Priority(+10000000)
)

type FairSorted struct {
	keyBase string
}

func NewFairSorted(keyBase string) *FairSorted {
	return &FairSorted{keyBase: keyBase}
}

func (q *FairSorted) String() string {
	return q.keyBase
}

//go:embed lua/fair_sorted_push.lua
var luaFSPush string
var scriptFSPush = redis.NewScript(3, luaFSPush)

// Push adds the passed in task to our queue for execution
func (q *FairSorted) Push(rc redis.Conn, taskType string, ownerID int, task any, priority Priority) error {
	score := q.score(priority)

	taskBody, err := json.Marshal(task)
	if err != nil {
		return err
	}

	wrapper := &Task{Type: taskType, OwnerID: ownerID, Task: taskBody, QueuedOn: dates.Now()}
	marshaled := jsonx.MustMarshal(wrapper)

	_, err = scriptFSPush.Do(rc, q.activeKey(), q.pausedKey(), q.queueKey(ownerID), ownerID, marshaled, score)
	return err
}

func (q *FairSorted) activeKey() string {
	return fmt.Sprintf("%s:active", q.keyBase)
}

func (q *FairSorted) pausedKey() string {
	return fmt.Sprintf("%s:paused", q.keyBase)
}

func (q *FairSorted) queueKey(ownerID int) string {
	return fmt.Sprintf("%s:%d", q.keyBase, ownerID)
}

func (q *FairSorted) score(priority Priority) string {
	s := float64(dates.Now().UnixMicro())/float64(1_000_000) + float64(priority)
	return strconv.FormatFloat(s, 'f', 6, 64)
}

//go:embed lua/fair_sorted_pop.lua
var luaFSPop string
var scriptFSPop = redis.NewScript(1, luaFSPop)

// Pop pops the next task off our queue
func (q *FairSorted) Pop(rc redis.Conn) (*Task, error) {
	task := &Task{}
	for {
		values, err := redis.Strings(scriptFSPop.Do(rc, q.activeKey(), q.keyBase))
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
var scriptFSDone = redis.NewScript(2, luaFSDone)

// Done marks the passed in task as complete. Callers must call this in order
// to maintain fair workers across orgs
func (q *FairSorted) Done(rc redis.Conn, ownerID int) error {
	_, err := scriptFSDone.Do(rc, q.activeKey(), q.pausedKey(), strconv.FormatInt(int64(ownerID), 10))
	return err
}

//go:embed lua/fair_sorted_size.lua
var luaFSSize string
var scriptFSSize = redis.NewScript(2, luaFSSize)

// Size returns the total number of tasks for the passed in queue across all owners
func (q *FairSorted) Size(rc redis.Conn) (int, error) {
	return redis.Int(scriptFSSize.Do(rc, q.activeKey(), q.pausedKey(), q.keyBase))
}

//go:embed lua/fair_sorted_pause.lua
var luaFSPause string
var scriptFSPause = redis.NewScript(2, luaFSPause)

func (q *FairSorted) Pause(rc redis.Conn, ownerID int) error {
	_, err := scriptFSPause.Do(rc, q.activeKey(), q.pausedKey(), strconv.FormatInt(int64(ownerID), 10))
	return err
}

//go:embed lua/fair_sorted_resume.lua
var luaFSResume string
var scriptFSResume = redis.NewScript(2, luaFSResume)

func (q *FairSorted) Resume(rc redis.Conn, ownerID int) error {
	_, err := scriptFSResume.Do(rc, q.activeKey(), q.pausedKey(), strconv.FormatInt(int64(ownerID), 10))
	return err
}
