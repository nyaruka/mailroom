package queues

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"time"
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

// fair implements a fair queue where tasks are distributed evenly across owners, each of which can only have a limited
// number of active tasks at a time. A popped task holds one of its owner's active slots under a lease until the
// consumer marks it done. If the consumer dies without doing so, the lease eventually expires and the slot is released
// by a later pop, so owners can't be starved of slots by tasks which will never complete. The task itself is lost.
//
// Other services push tasks directly onto these queues using their own implementation of push, so the key layout,
// payload framing and push behaviour must remain compatible with that. Older versions of this implementation, which
// pop without leases and mark tasks done by owner, may also still be consuming from the same queues - so owners' active
// counts may include slots held without leases.
//
// A queue with base key "foo" and owners "owner1" and "owner2" will have the following keys:
//   - {foo}:queued - set of owners scored by number of queued tasks
//   - {foo}:active - set of owners scored by number of active tasks
//   - {foo}:paused - set of paused owners
//   - {foo}:leases - hash of leased task IDs to their owners
//   - {foo}:expires - set of leased task IDs scored by lease expiry time (millis)
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
	maxActivePerOwner int           // max number of active tasks per owner
	lease             time.Duration // how long a popped task holds its owner's slot unless marked done
}

func newFair(keyBase string, maxActivePerOwner int, lease time.Duration) *fair {
	return &fair{keyBase: keyBase, maxActivePerOwner: maxActivePerOwner, lease: lease}
}

// expiredLease is a task whose lease expired and had its owner's slot released
type expiredLease struct {
	ID    TaskID
	Owner OwnerID
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

//go:embed lua/fair_pop.lua
var luaFairPop string
var scriptFairPop = valkey.NewScript(6, luaFairPop)

// max number of expired leases released by each pop
const reapLimit = 100

// pop pops the next task off our queue, returning an empty ID if there are no tasks available. It also returns any
// expired leases it released.
func (q *fair) pop(ctx context.Context, vc valkey.Conn) (TaskID, OwnerID, []byte, []expiredLease, error) {
	var expired []expiredLease

	for {
		vals, err := valkey.Values(scriptFairPop.DoContext(ctx, vc,
			q.queuedKey(), q.activeKey(), q.pausedKey(), q.tempKey(), q.leasesKey(), q.expiresKey(),
			q.keyBase, q.maxActivePerOwner, q.lease.Milliseconds(), reapLimit,
		))
		if err != nil {
			return "", "", nil, expired, fmt.Errorf("error popping task: %w", err)
		}

		var status, id, owner string
		var task []byte
		var reapedVals []any
		if _, err := valkey.Scan(vals, &status, &id, &owner, &task, &reapedVals); err != nil {
			return "", "", nil, expired, fmt.Errorf("error reading popped task: %w", err)
		}
		reaped, err := valkey.Strings(reapedVals, nil)
		if err != nil {
			return "", "", nil, expired, fmt.Errorf("error reading expired leases: %w", err)
		}

		for i := 0; i < len(reaped); i += 2 {
			expired = append(expired, expiredLease{ID: TaskID(reaped[i]), Owner: OwnerID(reaped[i+1])})
		}

		switch status {
		case "task":
			if !idRegex.MatchString(id) {
				q.done(ctx, vc, TaskID(id)) // release its slot now rather than when its lease expires
				return "", "", nil, expired, fmt.Errorf("invalid task ID for owner %s: %s", owner, id)
			}
			return TaskID(id), OwnerID(owner), task, expired, nil
		case "none":
			return "", "", nil, expired, nil
		case "invalid":
			return "", "", nil, expired, fmt.Errorf("invalid task payload for owner %s: %s", owner, task)
		}

		// selected owner turned out to have no queued tasks, so go back around again
	}
}

//go:embed lua/fair_done.lua
var luaFairDone string
var scriptFairDone = valkey.NewScript(3, luaFairDone)

// done marks the given task as complete, releasing its owner's slot. Returns false if the task's lease had already
// expired, in which case its slot was already released.
func (q *fair) done(ctx context.Context, vc valkey.Conn, id TaskID) (bool, error) {
	released, err := valkey.Bool(scriptFairDone.DoContext(ctx, vc, q.activeKey(), q.leasesKey(), q.expiresKey(), string(id)))
	if err != nil {
		return false, fmt.Errorf("error marking task %s done: %w", id, err)
	}
	return released, nil
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
var scriptFairDump = valkey.NewScript(4, luaFairDump)

func (q *fair) dump(ctx context.Context, vc valkey.Conn) ([]byte, error) {
	dump, err := valkey.Bytes(scriptFairDump.Do(vc, q.queuedKey(), q.activeKey(), q.pausedKey(), q.leasesKey()))
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

func (q *fair) leasesKey() string {
	return fmt.Sprintf("{%s}:leases", q.keyBase)
}

func (q *fair) expiresKey() string {
	return fmt.Sprintf("{%s}:expires", q.keyBase)
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
