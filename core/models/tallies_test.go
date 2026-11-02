package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/tallies"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTallyStore(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	org1, org2 := tallies.OwnerID(testdb.Org1.ID), tallies.OwnerID(testdb.Org2.ID)
	store := models.NewTallyStore(rt.DB, []models.TallyRetention{{Prefix: "daily/", Period: 30 * 24 * time.Hour}})
	flusher := tallies.NewFlusher(store, 10)

	increment := func(owner tallies.OwnerID, amounts map[string]int64) {
		require.NoError(t, tallies.Increment(ctx, vc, owner, amounts))
	}
	flush := func(expected int) {
		flushed, err := flusher.Flush(ctx, vc)
		require.NoError(t, err)
		assert.Equal(t, expected, flushed)
	}
	assertTallies := func(owner tallies.OwnerID, expected map[string]int) {
		rows, err := rt.DB.QueryContext(ctx, `SELECT scope, count FROM orgs_tally WHERE org_id = $1`, owner)
		require.NoError(t, err)
		defer rows.Close()

		actual := map[string]int{}
		for rows.Next() {
			var scope string
			var count int
			require.NoError(t, rows.Scan(&scope, &count))
			actual[scope] = count
		}
		assert.Equal(t, expected, actual)
	}
	assertGeneration := func(owner tallies.OwnerID, expected int64) {
		gen, err := store.LastGeneration(ctx, owner)
		require.NoError(t, err)
		assert.Equal(t, expected, gen)
	}

	assertGeneration(org1, 0)

	increment(org1, map[string]int64{"foo/1": 2, "daily/2025-05-04": 1})
	increment(org1, map[string]int64{"foo/1": 1})
	increment(org2, map[string]int64{"foo/1": 5})

	flush(2)

	assertTallies(org1, map[string]int{"foo/1": 3, "daily/2025-05-04": 1, "_gen": 1})
	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})
	assertGeneration(org1, 1)

	// only scopes matching a retention get an expiry, counted from their date
	var expiresOn []string
	require.NoError(t, rt.DB.SelectContext(ctx, &expiresOn, `SELECT COALESCE(expires_on::text, '') FROM orgs_tally WHERE org_id = $1 AND scope != '_gen' ORDER BY scope`, org1))
	assert.Equal(t, []string{"2025-06-03", ""}, expiresOn)

	increment(org1, map[string]int64{"foo/1": 1, "foo/2": 1, "daily/2025-05-04": 1, "daily/notadate": 1})

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 4, "foo/2": 1, "daily/2025-05-04": 2, "daily/notadate": 1, "_gen": 2})
	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})

	// a scope matching a retention but without a date gets no expiry
	var notADateExpiry *string
	require.NoError(t, rt.DB.GetContext(ctx, &notADateExpiry, `SELECT expires_on::text FROM orgs_tally WHERE org_id = $1 AND scope = 'daily/notadate'`, org1))
	assert.Nil(t, notADateExpiry)

	// writing a generation which has already been written does nothing
	require.NoError(t, store.Write(ctx, org1, 2, map[string]int64{"foo/1": 10}))
	assertTallies(org1, map[string]int{"foo/1": 4, "foo/2": 1, "daily/2025-05-04": 2, "daily/notadate": 1, "_gen": 2})

	// a flush which committed but failed before deleting its hash leaves it behind, and it's discarded rather than
	// written again
	vc.Do("HSET", "tallies:1:flushing", "foo/1", 10, "_gen", 2)
	increment(org1, map[string]int64{"foo/1": 1})

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 5, "foo/2": 1, "daily/2025-05-04": 2, "daily/notadate": 1, "_gen": 3})

	// a flush which failed before committing leaves its hash behind too, and new increments are merged into it
	vc.Do("HSET", "tallies:1:flushing", "foo/1", 10, "_gen", 4)
	vc.Do("SADD", "tallies:pending", 1)

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 15, "foo/2": 1, "daily/2025-05-04": 2, "daily/notadate": 1, "_gen": 4})

	// nothing is written for a deleted org, including from a hash left over from a failed flush
	rt.DB.MustExec(`UPDATE orgs_org SET deleted_on = NOW() WHERE id = $1`, org2)
	vc.Do("HSET", "tallies:2:flushing", "foo/1", 10, "_gen", 2)
	increment(org2, map[string]int64{"foo/1": 1})

	flush(1)

	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})
	assertvk.NotExists(t, vc, "tallies:2")
	assertvk.NotExists(t, vc, "tallies:2:flushing")
	assertvk.SMembers(t, vc, "tallies:pending", []string{})
}
