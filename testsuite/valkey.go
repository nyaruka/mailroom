package testsuite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/require"
)

// Each test claims its own valkey database, so no test ever sees another's state - whether from an earlier
// test in this binary or from a concurrently running one, as worktrees share a valkey. Databases 0-15 are
// reserved for development services; tests claim from 16-31, so the server must be configured with at least
// 32 databases. A claim is an advisory lock in Postgres held by the same per-binary connection that guards
// our test databases, so it evaporates exactly when the run that owns it dies - and a database is flushed
// when claimed, clearing anything a dead run left behind.

const (
	// DSN format for a claimed valkey database
	vkTestDSNFormat = "valkey://valkey:6379/%d"

	// the range tests claim from, leaving 0-15 to development services
	vkTestDBMin = 16
	vkTestDBMax = 31
)

// vkKey derives the advisory lock key which guards a given valkey database
func vkKey(n int) int64 {
	return dbKey(fmt.Sprintf("valkey_%d", n))
}

// databases claimed by this binary. The advisory locks alone can't guard against concurrent claims within
// this process (e.g. tests using t.Parallel) because they're re-entrant within our single owner session -
// both claimers would be told they got the lock.
var vkClaimed = make(map[int]bool)
var vkClaimedMutex sync.Mutex

// claimVKDB claims a valkey database for the duration of a test, flushes it, and returns its number.
// If every database is claimed - concurrently running binaries can saturate the pool - it waits for one
// to free up, rather than failing.
func claimVKDB(t *testing.T) int {
	t.Helper()

	deadline := time.Now().Add(time.Minute)
	for {
		if n := tryClaimVKDB(t); n != 0 {
			return n
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for an unclaimed valkey database in %d-%d", vkTestDBMin, vkTestDBMax)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// tryClaimVKDB makes one pass over the pool, returning the claimed database number or zero if none is free
func tryClaimVKDB(t *testing.T) int {
	t.Helper()

	ctx := context.Background()

	owner, err := dbOwner()
	require.NoError(t, err)

	for n := vkTestDBMin; n <= vkTestDBMax; n++ {
		vkClaimedMutex.Lock()
		if vkClaimed[n] {
			vkClaimedMutex.Unlock()
			continue // in use by another test in this binary
		}
		vkClaimed[n] = true
		vkClaimedMutex.Unlock()

		var got bool
		err := owner.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, vkKey(n)).Scan(&got)
		require.NoError(t, err, "error trying claim on valkey database")
		if !got {
			vkClaimedMutex.Lock()
			delete(vkClaimed, n)
			vkClaimedMutex.Unlock()
			continue // in use by a concurrently running binary
		}

		flushVKDB(t, n)

		t.Cleanup(func() {
			flushVKDB(t, n)

			_, err := owner.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, vkKey(n))
			require.NoError(t, err)

			vkClaimedMutex.Lock()
			delete(vkClaimed, n)
			vkClaimedMutex.Unlock()
		})

		return n
	}

	return 0
}

// flushVKDB clears out the given valkey database
func flushVKDB(t *testing.T, n int) {
	t.Helper()

	vc, err := valkey.DialURL(fmt.Sprintf(vkTestDSNFormat, n))
	require.NoError(t, err, "error connecting to valkey")
	defer vc.Close()

	_, err = vc.Do("FLUSHDB")
	require.NoError(t, err, "error flushing valkey database")
}
