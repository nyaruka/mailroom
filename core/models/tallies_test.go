package models_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/tallies"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTallyStore(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer dates.SetNowFunc(time.Now)
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2025, 5, 4, 23, 30, 0, 0, time.UTC)))

	org1, org2 := tallies.OwnerID(testdb.Org1.ID), tallies.OwnerID(testdb.Org2.ID)
	store := models.NewTallyStore(rt.DB, "tallies")
	other := models.NewTallyStore(rt.DB, "other")

	assertLastBatch := func(s *models.TallyStore, expected int64) {
		lastBatch, err := s.LastBatch(ctx)
		require.NoError(t, err)
		assert.Equal(t, expected, lastBatch)
	}
	write := func(id int64, amounts []tallies.Amount, expected bool) {
		written, err := store.Write(ctx, id, amounts)
		require.NoError(t, err)
		assert.Equal(t, expected, written)
	}
	assertTallies := func(owner tallies.OwnerID, expected map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT scope, count FROM orgs_tally WHERE org_id = $1`, owner).Map(expected)
	}
	assertExpiries := func(owner tallies.OwnerID, expected map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT scope, expires_on::text FROM orgs_tally WHERE org_id = $1`, owner).Map(expected)
	}

	assertLastBatch(store, 0)

	write(1, []tallies.Amount{
		{Owner: org1, Scope: "foo", Count: 3},
		{Owner: org1, Scope: "emails/shared@2025-05-04", Count: 1},
		{Owner: org1, Scope: "channels/12/text:in@2025-05-04", Count: 2},
		{Owner: org1, Scope: "msgs/text:in@2025-05-04", Count: 2},
		{Owner: org2, Scope: "foo", Count: 5},
	}, true)

	assertLastBatch(store, 1)
	assertLastBatch(other, 0)
	assertTallies(org1, map[string]any{
		"foo": int64(3), "emails/shared@2025-05-04": int64(1), "channels/12/text:in@2025-05-04": int64(2),
		"msgs/text:in@2025-05-04": int64(2),
	})
	assertTallies(org2, map[string]any{"foo": int64(5)})

	// expiries are from the day a tally is first written, by its retention
	assertExpiries(org1, map[string]any{
		"foo": nil, "emails/shared@2025-05-04": "2025-08-02", "channels/12/text:in@2025-05-04": "2027-05-04",
		"msgs/text:in@2025-05-04": nil,
	})

	// and aren't changed by later writes
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2025, 5, 10, 12, 0, 0, 0, time.UTC)))

	write(2, []tallies.Amount{
		{Owner: org1, Scope: "emails/shared@2025-05-04", Count: 2},
		{Owner: org1, Scope: "llms/foo/tokens:in@2025-05-10", Count: 100},
	}, true)

	assertTallies(org1, map[string]any{
		"foo": int64(3), "emails/shared@2025-05-04": int64(3), "channels/12/text:in@2025-05-04": int64(2),
		"msgs/text:in@2025-05-04": int64(2), "llms/foo/tokens:in@2025-05-10": int64(100),
	})
	assertExpiries(org1, map[string]any{
		"foo": nil, "emails/shared@2025-05-04": "2025-08-02", "channels/12/text:in@2025-05-04": "2027-05-04",
		"msgs/text:in@2025-05-04": nil, "llms/foo/tokens:in@2025-05-10": "2025-08-08",
	})

	// writing a batch which has already been written, or an earlier one, does nothing
	write(2, []tallies.Amount{{Owner: org1, Scope: "foo", Count: 10}}, false)
	write(1, []tallies.Amount{{Owner: org1, Scope: "foo", Count: 10}}, false)

	assertLastBatch(store, 2)
	assertTallies(org2, map[string]any{"foo": int64(5)})
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_tally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(3))

	// batch ids can skip ahead
	write(5, []tallies.Amount{{Owner: org1, Scope: "foo", Count: 1}}, true)
	assertLastBatch(store, 5)

	// nothing is written for an org which isn't active, or doesn't exist, but the rest of the batch is
	rt.DB.MustExec(`UPDATE orgs_org SET is_active = FALSE WHERE id = $1`, org2)

	write(6, []tallies.Amount{
		{Owner: org1, Scope: "foo", Count: 1},
		{Owner: org2, Scope: "foo", Count: 1},
		{Owner: 123456, Scope: "foo", Count: 1},
	}, true)

	assertLastBatch(store, 6)
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_tally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(5))
	assertTallies(org2, map[string]any{"foo": int64(5)})

	// a batch with nothing to write is still recorded
	write(7, []tallies.Amount{{Owner: org2, Scope: "foo", Count: 1}}, true)

	assertLastBatch(store, 7)
	assertTallies(org2, map[string]any{"foo": int64(5)})
}

func TestFlushTallies(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	store := models.NewTallyStore(rt.DB, rt.Tallies.KeyBase())

	org1, org2 := tallies.OwnerID(testdb.Org1.ID), tallies.OwnerID(testdb.Org2.ID)
	require.NoError(t, rt.Tallies.Increment(ctx, vc, org1, map[string]int64{"foo": 2, "bar": 1}))
	require.NoError(t, rt.Tallies.Increment(ctx, vc, org2, map[string]int64{"foo": 3}))

	flushed, err := rt.Tallies.Flush(ctx, vc, store, 2)
	require.NoError(t, err)
	assert.Equal(t, 3, flushed)

	assertdb.Query(t, rt.DB, `SELECT org_id || '|' || scope, count FROM orgs_tally`).Map(map[string]any{
		fmt.Sprintf("%d|bar", testdb.Org1.ID): int64(1),
		fmt.Sprintf("%d|foo", testdb.Org1.ID): int64(2),
		fmt.Sprintf("%d|foo", testdb.Org2.ID): int64(3),
	})
	assertdb.Query(t, rt.DB, `SELECT last_batch FROM orgs_tallyflush WHERE name = 'tallies'`).Returns(int64(2))
}
