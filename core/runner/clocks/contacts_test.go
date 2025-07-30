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
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
)

func TestLockContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, testsuite.ResetValkey)

	oa := testdb.Org1.Load(rt)

	// grab lock for contact 102
	locks, skipped, err := clocks.TryToLock(ctx, rt, oa, []models.ContactID{102}, time.Second)
	assert.NoError(t, err)
	assert.Len(t, locks, 1)
	assert.Len(t, skipped, 0)

	assertvk.Exists(t, vc, "lock:c:1:102")

	// try to get locks for 101, 102, 103
	locks, skipped, err = clocks.TryToLock(ctx, rt, oa, []models.ContactID{101, 102, 103}, time.Second)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []models.ContactID{101, 103}, slices.Collect(maps.Keys(locks)))
	assert.Equal(t, []models.ContactID{102}, skipped) // because it's already locked

	assertvk.Exists(t, vc, "lock:c:1:101")
	assertvk.Exists(t, vc, "lock:c:1:102")
	assertvk.Exists(t, vc, "lock:c:1:103")

	err = clocks.Unlock(ctx, rt, oa, locks)
	assert.NoError(t, err)

	assertvk.NotExists(t, vc, "lock:c:1:101")
	assertvk.Exists(t, vc, "lock:c:1:102")
	assertvk.NotExists(t, vc, "lock:c:1:103")

	// lock contacts 103 and 104 so only 101 is unlocked
	locks, skipped, err = clocks.TryToLock(ctx, rt, oa, []models.ContactID{103, 104}, time.Second)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []models.ContactID{103, 104}, slices.Collect(maps.Keys(locks)))
	assert.Len(t, skipped, 0)

	assertvk.NotExists(t, vc, "lock:c:1:101")
	assertvk.Exists(t, vc, "lock:c:1:102")
	assertvk.Exists(t, vc, "lock:c:1:103")
	assertvk.Exists(t, vc, "lock:c:1:104")

	// create a new context with a 2 second time limit
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	start := time.Now()

	// try to get locks for everyone.. we should get 101 instantly but we'll run out of time waiting for the rest
	_, _, err = clocks.TryToLock(ctx2, rt, oa, []models.ContactID{101, 102, 103, 104}, time.Second)
	assert.EqualError(t, err, "error attempting to grab lock: error trying to get lock: context deadline exceeded")

	// call should have completed in just over the context deadline
	assert.Less(t, time.Since(start), time.Second*3)

	// since we errored, any locks we grabbed before the error, should have been released
	assertvk.NotExists(t, vc, "lock:c:1:101")
}
