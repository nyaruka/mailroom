package sockets_test

import (
	"testing"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscribe(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// a released (inactive) contact in org1 - should be denied just like a non-existent one
	released := testdb.InsertContact(
		t, rt, testdb.Org1, "11111111-1111-4111-8111-111111111111", "Released", "eng", models.ContactStatusActive,
	)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, released.ID)

	testsuite.RunWebTests(t, rt, "testdata/subscribe.json")

	// the one allowed subscribe recorded its connection in the channel's index...
	assert.Equal(t, []string{"conn-allowed"}, subMembers(t, rt, "chat:a393abc0-283d-4c9b-a1b3-641a035c34bf"))

	// ...and denied subscribes recorded nothing
	assert.Empty(t, subMembers(t, rt, "chat:f6d20b72-f7d8-44dc-87f2-aae046dbff95"))
}

func TestSubRefresh(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testsuite.RunWebTests(t, rt, "testdata/sub_refresh.json")

	// the successful refresh recorded/extended the connection in the channel's index
	assert.Equal(t, []string{"conn-allowed"}, subMembers(t, rt, "chat:a393abc0-283d-4c9b-a1b3-641a035c34bf"))
}

// subMembers returns the connection ids currently indexed as subscribed to the given channel.
func subMembers(t *testing.T, rt *runtime.Runtime, channel string) []string {
	vc := rt.VK.Get()
	defer vc.Close()

	members, err := valkey.Strings(vc.Do("ZRANGE", "subs:"+channel, 0, -1))
	require.NoError(t, err)
	return members
}
