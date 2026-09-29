package queues

import (
	"fmt"
	"testing"
	"time"

	gqueues "github.com/nyaruka/gocommon/queues"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Other services push onto our queues using gocommon's queue implementation, so pushes from either must produce the
// same state, and tasks pushed by either must be poppable by us.
func TestFairCompatibility(t *testing.T) {
	ctx := t.Context()
	vc := assertvk.ClaimDB(t).Pool().Get()
	defer vc.Close()

	ours := newFair("test", 10, time.Minute)
	theirs := gqueues.NewFairV2("test", 10)

	pushTheirs := func(owner OwnerID, priority bool, task string) TaskID {
		id, err := theirs.Push(ctx, vc, gqueues.OwnerID(owner), priority, []byte(task))
		require.NoError(t, err)
		return TaskID(id)
	}

	id1 := pushTheirs("1", false, `{"a":1}`)
	id2 := assertPush(t, ours, vc, "1", false, []byte(`{"b":2}`))
	id3 := pushTheirs("1", true, `{"c":3}`)
	id4 := assertPush(t, ours, vc, "1", true, []byte(`{"d":4}`))
	id5 := pushTheirs("2", false, `{"e":"|"}`)

	assertvk.ZGetAll(t, vc, "{test}:queued", map[string]float64{"1": 4, "2": 1})
	assertvk.LGetAll(t, vc, "{test}:o:1/0", []string{string(id1) + `|{"a":1}`, string(id2) + `|{"b":2}`})
	assertvk.LGetAll(t, vc, "{test}:o:1/1", []string{string(id3) + `|{"c":3}`, string(id4) + `|{"d":4}`})
	assertvk.LGetAll(t, vc, "{test}:o:2/0", []string{string(id5) + `|{"e":"|"}`})

	// their pushes respect our pauses
	require.NoError(t, ours.pause(ctx, vc, "2"))
	pushTheirs("2", false, `{"f":6}`)
	assertvk.ZGetAll(t, vc, "{test}:queued", map[string]float64{"1": 4, "2": 2})

	assertPop(t, ours, vc, id3, "1", `{"c":3}`)
	assertPop(t, ours, vc, id4, "1", `{"d":4}`)
	assertPop(t, ours, vc, id1, "1", `{"a":1}`)
	assertPop(t, ours, vc, id2, "1", `{"b":2}`)
	assertPop(t, ours, vc, "", "", "") // owner 2 is paused

	require.NoError(t, ours.resume(ctx, vc, "2"))

	assertPop(t, ours, vc, id5, "2", `{"e":"|"}`)

	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{"1": 4, "2": 1})
	assertvk.HLen(t, vc, "{test}:leases", 5)

	size, err := ours.size(ctx, vc, "2")
	assert.NoError(t, err)
	assert.Equal(t, 1, size)
}

// Older versions of our queue popped without leases and marked tasks done by owner, and they can be consuming from the
// same queues as us during a deployment. gocommon's implementation is the same as those older versions.
func TestFairMixedVersions(t *testing.T) {
	ctx := t.Context()
	vc := assertvk.ClaimDB(t).Pool().Get()
	defer vc.Close()

	ours := newFair("test", 3, 200*time.Millisecond)
	old := gqueues.NewFairV2("test", 3)

	for i := range 6 {
		assertPush(t, ours, vc, "1", false, []byte(fmt.Sprintf(`{"t":%d}`, i)))
	}

	popOld := func() {
		_, owner, task, err := old.Pop(ctx, vc)
		require.NoError(t, err)
		require.NotNil(t, task)
		assert.Equal(t, gqueues.OwnerID("1"), owner)
	}
	popOurs := func() TaskID {
		id, _, _, _, err := ours.pop(ctx, vc)
		require.NoError(t, err)
		require.NotEqual(t, TaskID(""), id)
		return id
	}

	// both count towards the owner's active tasks, but only ours are leased
	popOld()
	id1 := popOurs()
	popOld()

	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{"1": 3})
	assertvk.HGetAll(t, vc, "{test}:leases", map[string]string{string(id1): "1"})
	assertvk.ZGetAll(t, vc, "{test}:queued", map[string]float64{"1": 3})

	// owner is at max active tasks for both
	_, _, task, err := old.Pop(ctx, vc)
	require.NoError(t, err)
	assert.Nil(t, task)
	assertPop(t, ours, vc, "", "", "")

	// each marks its own tasks done
	require.NoError(t, old.Done(ctx, vc, "1"))
	assertDone(t, ours, vc, id1, true)
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{"1": 1})

	id2 := popOurs()
	popOld()
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{"1": 3})

	// our leased task is abandoned.. when its lease expires only its slot is released, not those held by old poppers
	time.Sleep(250 * time.Millisecond)

	id3 := popOurs()
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{"1": 3})
	assertvk.HGetAll(t, vc, "{test}:leases", map[string]string{string(id3): "1"})
	assertDone(t, ours, vc, id2, false)

	require.NoError(t, old.Done(ctx, vc, "1"))
	require.NoError(t, old.Done(ctx, vc, "1"))
	assertDone(t, ours, vc, id3, true)

	assertvk.ZGetAll(t, vc, "{test}:queued", map[string]float64{})
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{})
	assertvk.HLen(t, vc, "{test}:leases", 0)
	assertvk.ZCard(t, vc, "{test}:expires", 0)
}
