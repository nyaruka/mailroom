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

	// a released (inactive) contact in org1 - should be denied just like a non-existent one
	released := testdb.InsertContact(
		t, rt, testdb.Org1, "11111111-1111-4111-8111-111111111111", "Released", "eng", models.ContactStatusActive,
	)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, released.ID)

	testsuite.RunWebTests(t, rt, "testdata/subscribe.json")

	vc := rt.VK.Get()
	defer vc.Close()

	// the one allowed subscribe recorded its connection in the channel's index...
	assertvk.ZRange(t, vc, subsKey("chat:a393abc0-283d-4c9b-a1b3-641a035c34bf"), 0, -1, []string{"conn-allowed"})

	// ...and denied subscribes recorded nothing
	assertvk.NotExists(t, vc, subsKey("chat:f6d20b72-f7d8-44dc-87f2-aae046dbff95"))
}

func TestSubRefresh(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testsuite.RunWebTests(t, rt, "testdata/sub_refresh.json")

	vc := rt.VK.Get()
	defer vc.Close()

	// the successful refresh recorded/extended the connection in the channel's index
	assertvk.ZRange(t, vc, subsKey("chat:a393abc0-283d-4c9b-a1b3-641a035c34bf"), 0, -1, []string{"conn-allowed"})
}

func TestSubscriptionIndex(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()

	t0 := time.Date(2025, 5, 4, 12, 0, 0, 0, time.UTC)
	channel := "chat:a393abc0-283d-4c9b-a1b3-641a035c34bf"
	key := subsKey(channel)

	// subscribing connection A records it scored at now+TTL, with a key TTL backstop
	require.NoError(t, indexSubscription(ctx, rt, t0, channel, "connA"))
	assertvk.ZGetAll(t, vc, key, map[string]float64{"connA": float64(t0.Add(subscribeTTL).Unix())})
	ttl, err := valkey.Int64(vc.Do("TTL", key))
	require.NoError(t, err)
	assert.Greater(t, ttl, int64(0))
	assert.LessOrEqual(t, ttl, int64(subscribeTTL/time.Second))

	// a second connection B to the same channel is added alongside A
	require.NoError(t, indexSubscription(ctx, rt, t0, channel, "connB"))
	assertvk.ZGetAll(t, vc, key, map[string]float64{
		"connA": float64(t0.Add(subscribeTTL).Unix()),
		"connB": float64(t0.Add(subscribeTTL).Unix()),
	})

	// refreshing A later extends its expiry score
	t1 := t0.Add(30 * time.Second)
	require.NoError(t, indexSubscription(ctx, rt, t1, channel, "connA"))
	assertvk.ZScore(t, vc, key, "connA", float64(t1.Add(subscribeTTL).Unix()))

	// a write well past B's expiry lazily prunes B while A (recently refreshed) survives
	t2 := t0.Add(subscribeTTL + time.Second)
	require.NoError(t, indexSubscription(ctx, rt, t2, channel, "connC"))
	assertvk.ZGetAll(t, vc, key, map[string]float64{
		"connA": float64(t1.Add(subscribeTTL).Unix()),
		"connC": float64(t2.Add(subscribeTTL).Unix()),
	})
}
