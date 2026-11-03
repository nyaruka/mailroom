package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
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
	store := models.NewTallyStore(rt.DB)
	day := time.Date(2025, 5, 4, 12, 0, 0, 0, time.UTC)

	increment := func(owner tallies.OwnerID, amounts map[tallies.Key]int64) {
		require.NoError(t, models.Tallies.Increment(ctx, vc, owner, amounts))
	}
	flush := func(expected int) {
		flushed, err := models.Tallies.Flush(ctx, vc, store, 10)
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
	assertExpiries := func(owner tallies.OwnerID, expected map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT scope, expires_on::text FROM orgs_tally WHERE org_id = $1 AND scope != '_gen'`, owner).Map(expected)
	}
	assertGeneration := func(owner tallies.OwnerID, expected int64) {
		gen, err := store.LastGeneration(ctx, owner)
		require.NoError(t, err)
		assert.Equal(t, expected, gen)
	}

	assertGeneration(org1, 0)

	increment(org1, map[tallies.Key]int64{
		tallies.Total("foo/1"):                       2,
		tallies.Daily("daily", day, 30*24*time.Hour): 1,
		tallies.Daily("forever", day, 0):             1,
	})
	increment(org1, map[tallies.Key]int64{tallies.Total("foo/1"): 1})
	increment(org2, map[tallies.Key]int64{tallies.Total("foo/1"): 5})

	flush(2)

	assertTallies(org1, map[string]int{"foo/1": 3, "daily@2025-05-04": 1, "forever@2025-05-04": 1, "_gen": 1})
	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})
	assertExpiries(org1, map[string]any{"foo/1": nil, "daily@2025-05-04": "2025-06-03", "forever@2025-05-04": nil})
	assertGeneration(org1, 1)

	// an expiry is only applied when a tally is created, so a later one for an existing tally is ignored
	increment(org1, map[tallies.Key]int64{
		tallies.Total("foo/1"):                        1,
		tallies.Total("foo/2"):                        1,
		tallies.Daily("daily", day, 60*24*time.Hour):  1,
		tallies.Daily("forever", day, 7*24*time.Hour): 1,
	})

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 4, "foo/2": 1, "daily@2025-05-04": 2, "forever@2025-05-04": 2, "_gen": 2})
	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})
	assertExpiries(org1, map[string]any{"foo/1": nil, "foo/2": nil, "daily@2025-05-04": "2025-06-03", "forever@2025-05-04": nil})

	// the scope which records the generation can't be written as a tally
	assert.EqualError(t, store.Write(ctx, org1, 3, map[string]int64{"_gen": 10}, nil), "_gen is a reserved scope")

	// writing a generation which has already been written does nothing
	require.NoError(t, store.Write(ctx, org1, 2, map[string]int64{"foo/1": 10, "foo/3": 1}, map[string]string{"foo/3": "2025-06-01"}))
	assertTallies(org1, map[string]int{"foo/1": 4, "foo/2": 1, "daily@2025-05-04": 2, "forever@2025-05-04": 2, "_gen": 2})

	// a flush which committed but failed before deleting its hash leaves it behind, and it's discarded rather than
	// written again
	vc.Do("HSET", "{tallies}:1:flushing", "foo/1", 10, "_gen", 2)
	increment(org1, map[tallies.Key]int64{tallies.Total("foo/1"): 1})

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 5, "foo/2": 1, "daily@2025-05-04": 2, "forever@2025-05-04": 2, "_gen": 3})

	// a flush which failed before committing leaves its hash behind too, and new increments are merged into it
	vc.Do("HSET", "{tallies}:1:flushing", "foo/1", 10, "_gen", 4)
	vc.Do("SADD", "{tallies}:pending", 1)

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 15, "foo/2": 1, "daily@2025-05-04": 2, "forever@2025-05-04": 2, "_gen": 4})

	// writes only apply the expiries of tallies they create
	require.NoError(t, store.Write(ctx, org1, 5, map[string]int64{"foo/1": 1, "foo/3": 1}, map[string]string{"foo/1": "2025-06-01", "foo/3": "2025-06-01"}))
	assertTallies(org1, map[string]int{"foo/1": 16, "foo/2": 1, "foo/3": 1, "daily@2025-05-04": 2, "forever@2025-05-04": 2, "_gen": 5})
	assertExpiries(org1, map[string]any{"foo/1": nil, "foo/2": nil, "foo/3": "2025-06-01", "daily@2025-05-04": "2025-06-03", "forever@2025-05-04": nil})

	// nothing is written for a deleted org, including from a hash left over from a failed flush
	rt.DB.MustExec(`UPDATE orgs_org SET deleted_on = NOW() WHERE id = $1`, org2)
	vc.Do("HSET", "{tallies}:2:flushing", "foo/1", 10, "_gen", 2)
	increment(org2, map[tallies.Key]int64{tallies.Total("foo/1"): 1})

	flush(1)

	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})
	assertvk.NotExists(t, vc, "{tallies}:2")
	assertvk.NotExists(t, vc, "{tallies}:2:flushing")
	assertvk.SMembers(t, vc, "{tallies}:pending", []string{})
}
