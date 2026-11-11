package models_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTallyField(t *testing.T) {
	day := time.Date(2025, 5, 4, 23, 30, 0, 0, time.FixedZone("", -2*3600))

	assert.Equal(t, "O1|foo", models.TallyField(testdb.Org1.ID, "foo", time.Time{}))
	assert.Equal(t, "O1|emails/shared@2025-05-05", models.TallyField(testdb.Org1.ID, "emails/shared", day))
	assert.Equal(t, "O2|foo@bar", models.TallyField(testdb.Org2.ID, "foo@bar", time.Time{}))
	assert.Panics(t, func() { models.TallyField(testdb.Org1.ID, "foo@bar", day) })

	assert.Equal(t, "C10000|foo", models.ChannelTallyField(testdb.TwilioChannel.ID, "foo", time.Time{}))
	assert.Equal(t, "C10000|text:in@2025-05-05", models.ChannelTallyField(testdb.TwilioChannel.ID, "text:in", day))
	assert.Panics(t, func() { models.ChannelTallyField(testdb.TwilioChannel.ID, "foo@bar", day) })

	assert.Equal(t, "F10000|segment:X9LlNwU0TBKEJb74evCdRgcrlbNhw04OjdHrdIEIP5Q",
		models.FlowTallyField(testdb.Favorites.ID, models.FlowSegmentTallyScope("5fd2e537-0534-4c12-8425-bef87af09d46", "072b95b3-61c3-4e0e-8dd1-eb7481083f94")))
}

func TestFlowSegmentTallyScope(t *testing.T) {
	// same vectors as temba's tests so that both sides agree on the encoding
	tcs := []struct {
		exit     flows.ExitUUID
		dest     core.NodeUUID
		expected string
	}{
		{"42af6e19-53ef-4fd9-8232-f202a3ea022b", "1d4f25b2-c648-46cc-8116-be0ec6b6d34a", "segment:Qq9uGVPvT9mCMvICo-oCKx1PJbLGSEbMgRa-Dsa200o"},
		{"2d7177e0-6bae-427f-9630-3823e2b8f1b2", "46681864-8389-4875-8b88-2f9fe241ea3f", "segment:LXF34GuuQn-WMDgj4rjxskZoGGSDiUh1i4gvn-JB6j8"},
		{"00000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff", "segment:AAAAAAAAAAAAAAAAAAAAAP____________________8"},
		{"fbfffbff-fbff-fbff-fbff-fbfffbfffbff", "3e3f3e3f-3e3f-3e3f-3e3f-3e3f3e3f3e3f", "segment:-__7__v_-__7__v_-__7_z4_Pj8-Pz4_Pj8-Pz4_Pj8"},
	}
	for _, tc := range tcs {
		assert.Equal(t, tc.expected, models.FlowSegmentTallyScope(tc.exit, tc.dest), "scope mismatch for %s -> %s", tc.exit, tc.dest)
	}
}

func TestFlowMsgsInTallies(t *testing.T) {
	handled := time.Date(2025, 5, 4, 23, 30, 0, 0, time.FixedZone("", -2*3600))

	assert.Equal(t, map[string]int64{
		"F10000|msgsin:hour:1":          1,
		"F10000|msgsin:date:2025-05-05": 1,
	}, models.FlowMsgsInTallies(testdb.Favorites.ID, handled))
}

func TestTallyStore(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer dates.SetNowFunc(time.Now)
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2025, 5, 4, 23, 30, 0, 0, time.UTC)))

	org1, org2 := testdb.Org1.ID, testdb.Org2.ID
	flow1, flow2 := testdb.Favorites.ID, testdb.PickANumber.ID
	store := models.NewTallyStore(rt.DB, "tallies")
	other := models.NewTallyStore(rt.DB, "other")

	assertLastBatch := func(s *models.TallyStore, expected int64) {
		lastBatch, err := s.LastBatch(ctx)
		require.NoError(t, err)
		assert.Equal(t, expected, lastBatch)
	}
	write := func(id int64, amounts map[string]int64, expected bool) {
		written, err := store.Write(ctx, id, amounts)
		require.NoError(t, err)
		assert.Equal(t, expected, written)
	}
	assertTallies := func(owner models.OrgID, expected map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT scope, count FROM orgs_orgtally WHERE org_id = $1`, owner).Map(expected)
	}
	assertExpiries := func(owner models.OrgID, expected map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT scope, expires_on::text FROM orgs_orgtally WHERE org_id = $1`, owner).Map(expected)
	}
	flowField := models.FlowTallyField
	assertFlowTallies := func(flowID models.FlowID, expected map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT scope, count FROM flows_flowtally WHERE flow_id = $1`, flowID).Map(expected)
	}

	assertLastBatch(store, 0)

	write(1, map[string]int64{
		models.TallyField(org1, "foo", time.Time{}):                            3,
		models.TallyField(org1, "emails/shared@2025-05-04", time.Time{}):       1,
		models.TallyField(org1, "channels/12/text:in@2025-05-04", time.Time{}): 2,
		models.TallyField(org1, "msgs/text:in@2025-05-04", time.Time{}):        2,
		models.TallyField(org2, "foo", time.Time{}):                            5,
	}, true)

	assertLastBatch(store, 1)
	assertLastBatch(other, 0)
	assertTallies(org1, map[string]any{
		"foo": int64(3), "emails/shared@2025-05-04": int64(1), "channels/12/text:in@2025-05-04": int64(2),
		"msgs/text:in@2025-05-04": int64(2),
	})
	assertTallies(org2, map[string]any{"foo": int64(5)})

	// expiries are from the day of a daily tally, by its retention
	assertExpiries(org1, map[string]any{
		"foo": nil, "emails/shared@2025-05-04": "2025-08-02", "channels/12/text:in@2025-05-04": "2026-06-08",
		"msgs/text:in@2025-05-04": nil,
	})

	// so don't depend on when it's written
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2025, 5, 10, 12, 0, 0, 0, time.UTC)))

	write(2, map[string]int64{
		models.TallyField(org1, "emails/shared@2025-05-04", time.Time{}):      2,
		models.TallyField(org1, "llms/foo/tokens:in@2025-05-10", time.Time{}): 100,
		models.TallyField(org1, "llms/foo/x:2025-01-01", time.Time{}):         1, // not a day as it's not after @
	}, true)

	assertTallies(org1, map[string]any{
		"foo": int64(3), "emails/shared@2025-05-04": int64(3), "channels/12/text:in@2025-05-04": int64(2),
		"msgs/text:in@2025-05-04": int64(2), "llms/foo/tokens:in@2025-05-10": int64(100), "llms/foo/x:2025-01-01": int64(1),
	})
	assertExpiries(org1, map[string]any{
		"foo": nil, "emails/shared@2025-05-04": "2025-08-02", "channels/12/text:in@2025-05-04": "2026-06-08",
		"msgs/text:in@2025-05-04": nil, "llms/foo/tokens:in@2025-05-10": "2025-08-08", "llms/foo/x:2025-01-01": "2025-08-08",
	})
	rt.DB.MustExec(`DELETE FROM orgs_orgtally WHERE org_id = $1 AND scope = 'llms/foo/x:2025-01-01'`, org1)

	// writing a batch which has already been written, or an earlier one, does nothing
	write(2, map[string]int64{models.TallyField(org1, "foo", time.Time{}): 10}, false)
	write(1, map[string]int64{models.TallyField(org1, "foo", time.Time{}): 10}, false)

	assertLastBatch(store, 2)
	assertTallies(org2, map[string]any{"foo": int64(5)})
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_orgtally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(3))

	// batch ids can skip ahead
	write(5, map[string]int64{models.TallyField(org1, "foo", time.Time{}): 1}, true)
	assertLastBatch(store, 5)
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_orgtally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(4))

	// flow tallies are written to their own table, with their own retentions, in the same batch as org tallies
	write(6, map[string]int64{
		models.TallyField(org1, "foo", time.Time{}):         5,
		flowField(flow1, "segment:a:b"):                     3,
		flowField(flow1, "emails/shared@2025-05-04"):        2,
		flowField(flow2, "segment:a:b"):                     1,
		flowField(flow2, "msgsin:hour:12"):                  2,
		flowField(flow2, "msgsin:date:2025-05-04"):          2,
		flowField(flow2, "msgsin:date:2024-05-04"):          1, // already past retention
		models.TallyField(org2, "segment:a:b", time.Time{}): 4,
	}, true)
	assertLastBatch(store, 6)
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_orgtally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(9))
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_orgtally WHERE org_id = $1 AND scope = 'segment:a:b'`, org2).Returns(int64(4))
	assertFlowTallies(flow1, map[string]any{"segment:a:b": int64(3), "emails/shared@2025-05-04": int64(2)})
	assertFlowTallies(flow2, map[string]any{"segment:a:b": int64(1), "msgsin:hour:12": int64(2), "msgsin:date:2025-05-04": int64(2)})
	assertdb.Query(t, rt.DB, `SELECT scope, expires_on::text FROM flows_flowtally WHERE expires_on IS NOT NULL`).Map(map[string]any{
		"msgsin:date:2025-05-04": "2026-05-04",
	})
	assertTallies(org2, map[string]any{"foo": int64(5), "segment:a:b": int64(4)})
	rt.DB.MustExec(`DELETE FROM orgs_orgtally WHERE org_id = $1 AND scope = 'segment:a:b'`, org2)

	// nothing is written for an org which isn't active, or doesn't exist, but the rest of the batch is
	rt.DB.MustExec(`UPDATE orgs_org SET is_active = FALSE WHERE id = $1`, org2)

	write(7, map[string]int64{
		models.TallyField(org1, "foo", time.Time{}):   1,
		models.TallyField(org2, "foo", time.Time{}):   1,
		models.TallyField(123456, "foo", time.Time{}): 1,
	}, true)

	assertLastBatch(store, 7)
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_orgtally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(10))
	assertTallies(org2, map[string]any{"foo": int64(5)})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM orgs_orgtally WHERE org_id = 123456`).Returns(0)

	// a batch with nothing to write is still recorded
	write(8, map[string]int64{models.TallyField(org2, "foo", time.Time{}): 1}, true)

	assertLastBatch(store, 8)
	assertTallies(org2, map[string]any{"foo": int64(5)})

	// a field which can't be parsed fails the whole batch
	for _, field := range []string{
		"foo", "1|foo", "x|foo", "X1|foo", "O|foo", "Ox|foo", "OO1|foo", "O-1|foo", "O0|foo", "F|foo", "Fx|foo", "FF1|foo", "F-1|foo",
		"C|foo", "C0|foo",
	} {
		written, err := store.Write(ctx, 9, map[string]int64{models.TallyField(org1, "foo", time.Time{}): 1, field: 1})
		assert.EqualError(t, err, fmt.Sprintf("invalid tally field %q", field))
		assert.False(t, written)
	}

	assertLastBatch(store, 8)
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_orgtally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(10))

	// zero totals aren't kept - a zero amount writes nothing and a decrement to zero deletes the tally
	write(9, map[string]int64{
		models.TallyField(org1, "foo", time.Time{}):                            -10,
		models.TallyField(org1, "bar", time.Time{}):                            0,
		models.TallyField(org1, "emails/shared@2025-05-04", time.Time{}):       -1,
		models.TallyField(org1, "channels/12/text:in@2025-05-04", time.Time{}): -2,
	}, true)

	assertLastBatch(store, 9)
	assertTallies(org1, map[string]any{
		"emails/shared@2025-05-04": int64(2), "msgs/text:in@2025-05-04": int64(2), "llms/foo/tokens:in@2025-05-10": int64(100),
	})

	// as does a batch whose fields for the same tally (here with a leading zero) net to zero, and it's still recorded
	write(10, map[string]int64{models.TallyField(org1, "bar", time.Time{}): 2, fmt.Sprintf("O0%d|bar", org1): -2}, true)

	assertLastBatch(store, 10)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM orgs_orgtally WHERE org_id = $1 AND scope = 'bar'`, org1).Returns(0)

	// a deleted tally which is written again gets the same expiry
	write(11, map[string]int64{models.TallyField(org1, "channels/12/text:in@2025-05-04", time.Time{}): 1}, true)

	assertExpiries(org1, map[string]any{
		"emails/shared@2025-05-04": "2025-08-02", "channels/12/text:in@2025-05-04": "2026-06-08",
		"msgs/text:in@2025-05-04": nil, "llms/foo/tokens:in@2025-05-10": "2025-08-08",
	})

	// nothing is written for a daily tally which is already past its retention
	write(12, map[string]int64{models.TallyField(org1, "emails/shared@2025-01-01", time.Time{}): 1}, true)

	assertLastBatch(store, 12)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM orgs_orgtally WHERE org_id = $1 AND scope = 'emails/shared@2025-01-01'`, org1).Returns(0)
	// or deleted for an org which isn't active
	rt.DB.MustExec(`INSERT INTO orgs_orgtally (org_id, scope, count) VALUES ($1, 'bar', 0)`, org2)

	write(13, map[string]int64{models.TallyField(org2, "bar", time.Time{}): -1}, true)

	assertTallies(org2, map[string]any{"foo": int64(5), "bar": int64(0)})

	// a flow tally decremented to zero is deleted, and nothing is written for a flow which isn't active or doesn't
	// exist, or an owner id beyond the range of the table's key, but the rest of the batch is
	rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE WHERE id = $1`, flow2)

	write(14, map[string]int64{
		flowField(flow1, "segment:a:b"):              -3,
		flowField(flow1, "emails/shared@2025-05-04"): 1,
		flowField(flow2, "segment:a:b"):              1,
		flowField(123456, "segment:a:b"):             1,
		flowField(1<<40, "segment:a:b"):              -1,
		models.TallyField(1<<40, "foo", time.Time{}): 1,
		models.TallyField(org1, "foo", time.Time{}):  1,
	}, true)

	assertLastBatch(store, 14)
	assertFlowTallies(flow1, map[string]any{"emails/shared@2025-05-04": int64(3)})
	assertFlowTallies(flow2, map[string]any{"segment:a:b": int64(1), "msgsin:hour:12": int64(2), "msgsin:date:2025-05-04": int64(2)})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowtally WHERE flow_id = 123456`).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count FROM orgs_orgtally WHERE org_id = $1 AND scope = 'foo'`, org1).Returns(int64(1))

	// channel tallies are written to their own table, all with the same retention from their day, and nothing is
	// written for a channel which isn't active or doesn't exist
	chField := func(id models.ChannelID, scope string) string { return fmt.Sprintf("C%d|%s", id, scope) }
	ch1, ch2, ch3 := testdb.TwilioChannel.ID, testdb.VonageChannel.ID, testdb.Org2Channel.ID
	assertChannelTallies := func(expected map[string]any) {
		assertdb.Query(t, rt.DB, `SELECT channel_id || '|' || scope || '|' || coalesce(expires_on::text, ''), count FROM channels_channeltally`).Map(expected)
	}
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, ch2)

	write(15, map[string]int64{
		chField(ch1, "text:in@2025-05-04"):    3,
		chField(ch1, "text:out@2025-05-04"):   2,
		chField(ch1, "text:in@2024-01-01"):    1, // already past retention
		chField(ch1, "foo"):                   1,
		chField(ch2, "text:in@2025-05-04"):    1,
		chField(ch3, "voice:in@2025-05-04"):   4,
		chField(123456, "text:in@2025-05-04"): 1,
	}, true)

	assertLastBatch(store, 15)
	assertChannelTallies(map[string]any{
		fmt.Sprintf("%d|text:in@2025-05-04|2026-06-08", ch1):  int64(3),
		fmt.Sprintf("%d|text:out@2025-05-04|2026-06-08", ch1): int64(2),
		fmt.Sprintf("%d|foo|2026-06-14", ch1):                 int64(1),
		fmt.Sprintf("%d|voice:in@2025-05-04|2026-06-08", ch3): int64(4),
	})

	// and a channel tally decremented to zero is deleted
	write(16, map[string]int64{chField(ch1, "text:in@2025-05-04"): -3, chField(ch3, "voice:in@2025-05-04"): 1}, true)

	assertChannelTallies(map[string]any{
		fmt.Sprintf("%d|text:out@2025-05-04|2026-06-08", ch1): int64(2),
		fmt.Sprintf("%d|foo|2026-06-14", ch1):                 int64(1),
		fmt.Sprintf("%d|voice:in@2025-05-04|2026-06-08", ch3): int64(5),
	})
}

func TestFlushTallies(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	store := models.NewTallyStore(rt.DB, rt.Tallies.KeyBase())

	org1, org2 := testdb.Org1.ID, testdb.Org2.ID
	require.NoError(t, rt.Tallies.Increment(ctx, vc, map[string]int64{
		models.TallyField(org1, "foo", time.Time{}): 2, models.TallyField(org1, "bar", time.Time{}): 1, models.TallyField(org2, "foo", time.Time{}): 3,
		fmt.Sprintf("F%d|segment:a:b", testdb.Favorites.ID): 4,
		fmt.Sprintf("C%d|foo", testdb.TwilioChannel.ID):     5,
	}))

	flushed, err := rt.Tallies.Flush(ctx, vc, store, 2)
	require.NoError(t, err)
	assert.Equal(t, 5, flushed)

	assertdb.Query(t, rt.DB, `SELECT org_id || '|' || scope, count FROM orgs_orgtally`).Map(map[string]any{
		fmt.Sprintf("%d|bar", testdb.Org1.ID): int64(1),
		fmt.Sprintf("%d|foo", testdb.Org1.ID): int64(2),
		fmt.Sprintf("%d|foo", testdb.Org2.ID): int64(3),
	})
	assertdb.Query(t, rt.DB, `SELECT flow_id || '|' || scope, count FROM flows_flowtally`).Map(map[string]any{
		fmt.Sprintf("%d|segment:a:b", testdb.Favorites.ID): int64(4),
	})
	assertdb.Query(t, rt.DB, `SELECT channel_id || '|' || scope, count FROM channels_channeltally`).Map(map[string]any{
		fmt.Sprintf("%d|foo", testdb.TwilioChannel.ID): int64(5),
	})
	assertdb.Query(t, rt.DB, `SELECT last_batch FROM orgs_tallyflush WHERE name = 'tallies'`).Returns(int64(3))
}
