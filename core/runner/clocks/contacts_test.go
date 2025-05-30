package clocks_test

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/clocks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/redisx/assertredis"
	"github.com/stretchr/testify/assert"
)

func TestLockContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetRedis)

	oa := testdb.Org1.Load(rt)

	// grab lock for contact 102
	locks, skipped, err := clocks.TryToLock(ctx, rt, oa, []models.ContactID{102}, time.Second)
	assert.NoError(t, err)
	assert.Len(t, locks, 1)
	assert.Len(t, skipped, 0)

	assertredis.Exists(t, rc, "lock:c:1:102")

	// try to get locks for 101, 102, 103
	locks, skipped, err = clocks.TryToLock(ctx, rt, oa, []models.ContactID{101, 102, 103}, time.Second)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []models.ContactID{101, 103}, slices.Collect(maps.Keys(locks)))
	assert.Equal(t, []models.ContactID{102}, skipped) // because it's already locked

	assertredis.Exists(t, rc, "lock:c:1:101")
	assertredis.Exists(t, rc, "lock:c:1:102")
	assertredis.Exists(t, rc, "lock:c:1:103")

	err = clocks.Unlock(rt, oa, locks)
	assert.NoError(t, err)

	assertredis.NotExists(t, rc, "lock:c:1:101")
	assertredis.Exists(t, rc, "lock:c:1:102")
	assertredis.NotExists(t, rc, "lock:c:1:103")

	// lock contacts 103, 104, 105 so only 101 is unlocked
	clocks.TryToLock(ctx, rt, oa, []models.ContactID{103}, time.Second)

	// create a new context with a 2 second timelimit
	ctx2, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	start := time.Now()

	_, _, err = clocks.TryToLock(ctx2, rt, oa, []models.ContactID{101, 102, 103, 104}, time.Second)
	assert.EqualError(t, err, "context deadline exceeded")

	// call should have completed in just over the context deadline
	assert.Less(t, time.Since(start), time.Second*3)

	// since we errored, any locks we grabbed before the error, should have been released
	assertredis.NotExists(t, rc, "lock:c:1:101")
}
