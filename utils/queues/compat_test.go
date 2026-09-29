package queues

import (
	"testing"

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

	ours := newFair("test", 10)
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

	size, err := ours.size(ctx, vc, "2")
	assert.NoError(t, err)
	assert.Equal(t, 1, size)
}
