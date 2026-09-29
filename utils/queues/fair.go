package queues

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"uuid"

	valkey "github.com/gomodule/redigo/redis"
)

// OwnerID is the identifier for an owner of tasks in the queue.
type OwnerID string

// newTaskID can be overridden in tests to generate predictable IDs
var newTaskID func() TaskID = defaultNewTaskID

func defaultNewTaskID() TaskID {
	return TaskID(uuid.NewV7().String())
}

// fair implements a fair queue where tasks are distributed evenly across owners. A popped task only exists as an
// active count against its owner, which consumers decrement by calling done, so a consumer dying mid-task loses the
// task.
//
// Other services push tasks directly onto these queues using their own implementation of push, so the key layout,
// payload framing and push behaviour must remain compatible with that.
//
// A queue with base key "foo" and owners "owner1" and "owner2" will have the following keys:
//   - {foo}:queued - set of owners scored by number of queued tasks
//   - {foo}:active - set of owners scored by number of active tasks
//   - {foo}:paused - set of paused owners
//   - {foo}:temp - used internally
//   - {foo}:o:owner1/0 - e.g. list of tasks for owner1 with priority 0 (low)
//   - {foo}:o:owner1/1 - e.g. list of tasks for owner1 with priority 1 (high)
//   - {foo}:o:owner2/0 - e.g. list of tasks for owner2 with priority 0 (low)
//   - {foo}:o:owner2/1 - e.g. list of tasks for owner2 with priority 1 (high)
//
// Note: it would be nice if owner queues could use distict hash tags and so live on different nodes in a cluster, but
// our push and pop scripts require atomic changes to the queued/active sets and the task lists.
type fair struct {
	keyBase           string
	maxActivePerOwner int // max number of active tasks per owner
}

func newFair(keyBase string, maxActivePerOwner int) *fair {
	return &fair{keyBase: keyBase, maxActivePerOwner: maxActivePerOwner}
}

//go:embed lua/fair_push.lua
var luaFairPush string
var scriptFairPush = valkey.NewScript(4, luaFairPush)

// push adds the passed in task to our queue for execution
func (q *fair) push(ctx context.Context, vc valkey.Conn, owner OwnerID, priority bool, task []byte) (TaskID, error) {
	id := newTaskID()

	// prepend UUID to the task
	var payload bytes.Buffer
	payload.WriteString(string(id))
	payload.WriteByte('|')
	payload.Write(task)

	queueKeys := q.queueKeys(owner)

	_, err := scriptFairPush.Do(vc, q.queuedKey(), q.activeKey(), queueKeys[0], queueKeys[1], owner, priority, payload.Bytes())
	if err != nil {
		return "", fmt.Errorf("error pushing task for owner %s: %w", owner, err)
	}
	return id, nil
}

//go:embed lua/fair_pop_owner.lua
var luaFairPopOwner string
var scriptFairPopOwner = valkey.NewScript(4, luaFairPopOwner)

//go:embed lua/fair_pop_task.lua
var luaFairPopTask string
var scriptFairPopTask = valkey.NewScript(3, luaFairPopTask)

// pop pops the next task off our queue
func (q *fair) pop(ctx context.Context, vc valkey.Conn) (TaskID, OwnerID, []byte, error) {
	for {
		// Select an owner with queued tasks
		owner, err := valkey.String(scriptFairPopOwner.DoContext(ctx, vc, q.queuedKey(), q.activeKey(), q.pausedKey(), q.tempKey(), q.maxActivePerOwner))
		if err != nil {
			return "", "", nil, fmt.Errorf("error selecting task owner: %w", err)
		}
		if owner == "" { // None found so no tasks to pop
			return "", "", nil, nil
		}

		// Pop a task for the owner
		queueKeys := q.queueKeys(OwnerID(owner))
		payload, err := valkey.String(scriptFairPopTask.DoContext(ctx, vc, q.activeKey(), queueKeys[0], queueKeys[1], owner))
		if err != nil {
			return "", "", nil, fmt.Errorf("error popping task for owner %s: %w", owner, err)
		}
		if payload != "" {
			id, task, err := parsePayload([]byte(payload))
			if err != nil {
				return "", "", nil, fmt.Errorf("error parsing task payload for owner %s: %w", owner, err)
			}

			return id, OwnerID(owner), task, nil
		}

		// It's possible that we selected an owner with no tasks, so go back around again
	}
}

//go:embed lua/fair_done.lua
var luaFairDone string
var scriptFairDone = valkey.NewScript(1, luaFairDone)

// done marks the passed in task as complete. Callers must call this in order to maintain fair workers across owners
func (q *fair) done(ctx context.Context, vc valkey.Conn, owner OwnerID) error {
	_, err := scriptFairDone.Do(vc, q.activeKey(), owner)
	if err != nil {
		return fmt.Errorf("error marking task done for owner %s: %w", owner, err)
	}
	return nil
}

// pause marks the given owner as paused, disabling processing of their tasks
func (q *fair) pause(ctx context.Context, vc valkey.Conn, owner OwnerID) error {
	_, err := valkey.DoContext(vc, ctx, "SADD", q.pausedKey(), owner)
	return err
}

// resume unmarks the given owner as paused, re-enabling processing of their tasks
func (q *fair) resume(ctx context.Context, vc valkey.Conn, owner OwnerID) error {
	_, err := valkey.DoContext(vc, ctx, "SREM", q.pausedKey(), owner)
	return err
}

// paused returns the list of owners marked as paused
func (q *fair) paused(ctx context.Context, vc valkey.Conn) ([]OwnerID, error) {
	strs, err := valkey.Strings(valkey.DoContext(vc, ctx, "SMEMBERS", q.pausedKey()))
	if err != nil {
		return nil, err
	}

	owners := make([]OwnerID, len(strs))
	for i, str := range strs {
		owners[i] = OwnerID(str)
	}

	return owners, nil
}

// queued returns the list of owners with queued tasks
func (q *fair) queued(ctx context.Context, vc valkey.Conn) ([]OwnerID, error) {
	strs, err := valkey.Strings(valkey.DoContext(vc, ctx, "ZRANGE", q.queuedKey(), 0, -1))
	if err != nil {
		return nil, err
	}

	owners := make([]OwnerID, len(strs))
	for i, str := range strs {
		owners[i] = OwnerID(str)
	}

	return owners, nil
}

// size returns the number of queued tasks for the given owner
func (q *fair) size(ctx context.Context, vc valkey.Conn, owner OwnerID) (int, error) {
	queueKeys := q.queueKeys(owner)

	vc.Send("MULTI")
	vc.Send("LLEN", queueKeys[0])
	vc.Send("LLEN", queueKeys[1])
	counts, err := valkey.Ints(valkey.DoContext(vc, ctx, "EXEC"))
	if err != nil {
		return 0, err
	}

	return counts[0] + counts[1], nil
}

//go:embed lua/fair_dump.lua
var luaFairDump string
var scriptFairDump = valkey.NewScript(3, luaFairDump)

func (q *fair) dump(ctx context.Context, vc valkey.Conn) ([]byte, error) {
	dump, err := valkey.Bytes(scriptFairDump.Do(vc, q.queuedKey(), q.activeKey(), q.pausedKey()))
	if err != nil {
		return nil, fmt.Errorf("error dumping queue state: %w", err)
	}

	return dump, nil
}

func (q *fair) queuedKey() string {
	return fmt.Sprintf("{%s}:queued", q.keyBase)
}

func (q *fair) activeKey() string {
	return fmt.Sprintf("{%s}:active", q.keyBase)
}

func (q *fair) pausedKey() string {
	return fmt.Sprintf("{%s}:paused", q.keyBase)
}

func (q *fair) tempKey() string {
	return fmt.Sprintf("{%s}:temp", q.keyBase)
}

func (q *fair) queueKeys(owner OwnerID) [2]string {
	return [2]string{
		fmt.Sprintf("{%s}:o:%s/0", q.keyBase, owner),
		fmt.Sprintf("{%s}:o:%s/1", q.keyBase, owner),
	}
}

var idRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-7][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func parsePayload(raw []byte) (TaskID, []byte, error) {
	if len(raw) == 0 {
		return "", nil, fmt.Errorf("empty task payload")
	}

	parts := bytes.SplitN(raw, []byte{'|'}, 2)
	if len(parts) != 2 || !idRegex.Match(parts[0]) {
		return "", nil, fmt.Errorf("invalid task payload: %s", raw)
	}

	return TaskID(parts[0]), parts[1], nil
}
