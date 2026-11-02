package tallies_test

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/utils/tallies"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaily(t *testing.T) {
	assert.Equal(t, "foo/2025-05-04", tallies.Daily("foo", time.Date(2025, 5, 4, 23, 59, 0, 0, time.UTC)))
	assert.Equal(t, "foo/2025-05-05", tallies.Daily("foo", time.Date(2025, 5, 4, 23, 0, 0, 0, time.FixedZone("", -2*3600))))
}

func TestFlush(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	org1, org2 := tallies.OrgID(testdb.Org1.ID), tallies.OrgID(testdb.Org2.ID)
	flusher := tallies.NewFlusher([]tallies.Retention{{Prefix: "daily/", Period: 30 * 24 * time.Hour}}, 10)

	increment := func(orgID tallies.OrgID, amounts map[string]int) {
		require.NoError(t, tallies.Increment(ctx, vc, orgID, amounts))
	}
	flush := func(expected int) {
		flushed, err := flusher.Flush(ctx, rt.DB, vc)
		require.NoError(t, err)
		assert.Equal(t, expected, flushed)
	}
	assertTallies := func(orgID tallies.OrgID, expected map[string]int) {
		rows, err := rt.DB.QueryContext(ctx, `SELECT scope, count FROM orgs_tally WHERE org_id = $1`, orgID)
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

	// nothing to flush
	flush(0)

	increment(org1, map[string]int{})
	assertvk.SMembers(t, vc, "tallies:pending", []string{})

	increment(org1, map[string]int{"foo/1": 2, "daily/2025-05-04": 1})
	increment(org1, map[string]int{"foo/1": 1})
	increment(org2, map[string]int{"foo/1": 5})

	assertvk.HGetAll(t, vc, "tallies:1", map[string]string{"foo/1": "3", "daily/2025-05-04": "1"})
	assertvk.SMembers(t, vc, "tallies:pending", []string{"1", "2"})

	flush(2)

	assertTallies(org1, map[string]int{"foo/1": 3, "daily/2025-05-04": 1, "_gen": 1})
	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})
	assertvk.NotExists(t, vc, "tallies:1")
	assertvk.NotExists(t, vc, "tallies:1:flushing")
	assertvk.SMembers(t, vc, "tallies:pending", []string{})

	// only scopes matching a retention get an expiry, counted from their date
	var expiresOn []string
	require.NoError(t, rt.DB.SelectContext(ctx, &expiresOn, `SELECT COALESCE(expires_on::text, '') FROM orgs_tally WHERE org_id = $1 AND scope != '_gen' ORDER BY scope`, org1))
	assert.Equal(t, []string{"2025-06-03", ""}, expiresOn)

	increment(org1, map[string]int{"foo/1": 1, "foo/2": 1, "daily/2025-05-04": 1, "daily/notadate": 1})

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 4, "foo/2": 1, "daily/2025-05-04": 2, "daily/notadate": 1, "_gen": 2})
	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})

	// a flush which committed but failed before deleting its hash leaves it behind, and it's discarded rather than
	// written again
	vc.Do("HSET", "tallies:1:flushing", "foo/1", 10, "_gen", 2)
	increment(org1, map[string]int{"foo/1": 1})

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 5, "foo/2": 1, "daily/2025-05-04": 2, "daily/notadate": 1, "_gen": 3})

	// a flush which failed before committing leaves its hash behind too, and new increments are merged into it
	vc.Do("HSET", "tallies:1:flushing", "foo/1", 10, "_gen", 4)
	vc.Do("SADD", "tallies:pending", 1)

	flush(1)

	assertTallies(org1, map[string]int{"foo/1": 15, "foo/2": 1, "daily/2025-05-04": 2, "daily/notadate": 1, "_gen": 4})

	// nothing is written for a deleted org
	rt.DB.MustExec(`UPDATE orgs_org SET deleted_on = NOW() WHERE id = $1`, org2)
	increment(org2, map[string]int{"foo/1": 1})

	flush(1)

	assertTallies(org2, map[string]int{"foo/1": 5, "_gen": 1})
	assertvk.NotExists(t, vc, "tallies:2")
	assertvk.SMembers(t, vc, "tallies:pending", []string{})
}
