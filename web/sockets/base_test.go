package sockets

import (
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscribe(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// a released (soft-deleted) contact in org1 - should be denied like a non-existent one
	released := testdb.InsertContact(
		t, rt, testdb.Org1, "11111111-1111-4111-8111-111111111111", "Released", "eng", models.ContactStatusActive,
	)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, released.ID)

	// a blocked contact is still authorized - blocked/stopped/archived contacts keep viewable chat history
	testdb.InsertContact(
		t, rt, testdb.Org1, "22222222-2222-4222-8222-222222222222", "Blocked", "eng", models.ContactStatusBlocked,
	)

	testsuite.RunWebTests(t, rt, "testdata/subscribe.json")

	vc := rt.VK.Get()
	defer vc.Close()

	annKey := subKey("chat:a393abc0-283d-4c9b-a1b3-641a035c34bf", "conn-allowed")
	blockedKey := subKey("chat:22222222-2222-4222-8222-222222222222", "conn-blocked")

	// the allowed subscribes each recorded their connection, valued with the subscribing user...
	assertvk.Get(t, vc, annKey, "ad9fdf9f-56ab-422a-b77d-e3ec26091a25")
	assertvk.Exists(t, vc, blockedKey)

	// ...and the denied/disconnected subscribes (other org, missing, malformed uuid, unknown namespace,
	// released contact, no/empty meta) wrote nothing - only the two allowed keys exist
	assertvk.Keys(t, vc, "socket-subs:*", []string{annKey, blockedKey})
}

func TestSubRefresh(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testsuite.RunWebTests(t, rt, "testdata/sub_refresh.json")

	vc := rt.VK.Get()
	defer vc.Close()

	// the successful refresh recorded/refreshed the connection's key; nothing else was written
	key := subKey("chat:a393abc0-283d-4c9b-a1b3-641a035c34bf", "conn-allowed")
	assertvk.Get(t, vc, key, "ad9fdf9f-56ab-422a-b77d-e3ec26091a25")
	assertvk.Keys(t, vc, "socket-subs:*", []string{key})
}

func TestSubscriptionIndex(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()

	channel := "chat:a393abc0-283d-4c9b-a1b3-641a035c34bf"

	// subscribing a connection writes its own key, valued with the user and expiring after the TTL
	require.NoError(t, indexSubscription(ctx, rt, channel, "connA", "user-a"))
	assertvk.Get(t, vc, subKey(channel, "connA"), "user-a")

	ttl, err := valkey.Int64(vc.Do("TTL", subKey(channel, "connA")))
	require.NoError(t, err)
	assert.Greater(t, ttl, int64(0))
	assert.LessOrEqual(t, ttl, int64(subscribeTTL/time.Second))

	// a second connection to the same channel is an independent key
	require.NoError(t, indexSubscription(ctx, rt, channel, "connB", "user-b"))
	assertvk.Keys(t, vc, "socket-subs:*", []string{subKey(channel, "connA"), subKey(channel, "connB")})

	// refreshing a connection keeps its key and resets the TTL
	require.NoError(t, indexSubscription(ctx, rt, channel, "connA", "user-a"))
	ttl, err = valkey.Int64(vc.Do("TTL", subKey(channel, "connA")))
	require.NoError(t, err)
	assert.Greater(t, ttl, int64(0))
	assert.LessOrEqual(t, ttl, int64(subscribeTTL/time.Second))
}
