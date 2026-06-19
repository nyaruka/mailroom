package sockets

import (
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionIndex(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	t0 := time.Date(2025, 5, 4, 12, 0, 0, 0, time.UTC)
	channel := "chat:a393abc0-283d-4c9b-a1b3-641a035c34bf"
	key := subsKey(channel)

	// subscribing connection A records it scored at now+TTL, with a key TTL backstop
	require.NoError(t, indexSubscription(ctx, rt, t0, channel, "connA"))
	assert.Equal(t, []string{"connA"}, zmembers(t, rt, key))
	assert.Equal(t, t0.Add(subscribeTTL).Unix(), zscore(t, rt, key, "connA"))
	assert.Greater(t, keyTTL(t, rt, key), int64(0))
	assert.LessOrEqual(t, keyTTL(t, rt, key), int64(subscribeTTL/time.Second))

	// a second connection B to the same channel is added alongside A
	require.NoError(t, indexSubscription(ctx, rt, t0, channel, "connB"))
	assert.Equal(t, []string{"connA", "connB"}, zmembers(t, rt, key))

	// refreshing A later extends its expiry score
	t1 := t0.Add(30 * time.Second)
	require.NoError(t, indexSubscription(ctx, rt, t1, channel, "connA"))
	assert.Equal(t, t1.Add(subscribeTTL).Unix(), zscore(t, rt, key, "connA"))

	// a write well past B's expiry lazily prunes B while A (recently refreshed) survives
	t2 := t0.Add(subscribeTTL + time.Second)
	require.NoError(t, indexSubscription(ctx, rt, t2, channel, "connC"))
	members := zmembers(t, rt, key)
	assert.NotContains(t, members, "connB")
	assert.Contains(t, members, "connA")
	assert.Contains(t, members, "connC")
}

func zmembers(t *testing.T, rt *runtime.Runtime, key string) []string {
	vc := rt.VK.Get()
	defer vc.Close()

	members, err := valkey.Strings(vc.Do("ZRANGE", key, 0, -1))
	require.NoError(t, err)
	return members
}

func zscore(t *testing.T, rt *runtime.Runtime, key, member string) int64 {
	vc := rt.VK.Get()
	defer vc.Close()

	score, err := valkey.Float64(vc.Do("ZSCORE", key, member))
	require.NoError(t, err)
	return int64(score)
}

func keyTTL(t *testing.T, rt *runtime.Runtime, key string) int64 {
	vc := rt.VK.Get()
	defer vc.Close()

	ttl, err := valkey.Int64(vc.Do("TTL", key))
	require.NoError(t, err)
	return ttl
}
