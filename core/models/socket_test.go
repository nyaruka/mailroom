package models_test

import (
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryChannel(t *testing.T) {
	assert.Equal(t, "history:a393abc0-283d-4c9b-a1b3-641a035c34bf", models.HistoryChannel("a393abc0-283d-4c9b-a1b3-641a035c34bf"))
}

func TestSubscriptions(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	vc := rt.VK.Get()
	defer vc.Close()

	const ttl = 150 * time.Second
	contact1 := flows.ContactUUID("a393abc0-283d-4c9b-a1b3-641a035c34bf")
	contact2 := flows.ContactUUID("b699a406-7e44-49be-9f01-1a82893e8a10")
	hist1 := models.HistoryChannel(contact1)
	hist2 := models.HistoryChannel(contact2)

	assertSubscribed := func(channel string, expected bool) {
		t.Helper()
		actual, err := models.IsSubscribed(ctx, rt, channel)
		require.NoError(t, err)
		assert.Equal(t, expected, actual, "subscribed mismatch for %s", channel)
	}

	// nothing subscribed yet
	assertSubscribed(hist1, false)

	// recording a subscription marks the channel present with a TTL
	require.NoError(t, models.RecordSubscription(ctx, rt, hist1, ttl))
	assertSubscribed(hist1, true)

	secs, err := valkey.Int64(vc.Do("TTL", "socket-subs:"+hist1))
	require.NoError(t, err)
	assert.Greater(t, secs, int64(0))
	assert.LessOrEqual(t, secs, int64(ttl/time.Second))

	// a second subscriber to the same channel keeps it a single key (we track presence, not who)
	require.NoError(t, models.RecordSubscription(ctx, rt, hist1, ttl))
	assertvk.Keys(t, vc, "socket-subs:*", []string{"socket-subs:" + hist1})

	// a different channel is a separate key, checked independently
	assertSubscribed(hist2, false)
	require.NoError(t, models.RecordSubscription(ctx, rt, hist2, ttl))
	assertSubscribed(hist2, true)
	assertvk.Keys(t, vc, "socket-subs:*", []string{"socket-subs:" + hist1, "socket-subs:" + hist2})
}
