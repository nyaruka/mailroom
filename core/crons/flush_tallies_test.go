package crons_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/tallies"
	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlushTalliesCron(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	cron := &crons.FlushTalliesCron{}

	day := time.Date(2025, 5, 4, 12, 0, 0, 0, time.UTC)
	require.NoError(t, models.Tallies.Increment(ctx, vc, tallies.OwnerID(testdb.Org1.ID), map[tallies.Key]int64{
		tallies.Daily("emails/shared", day, 90*24*time.Hour): 2,
		tallies.Total("foo"): 3,
	}))

	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"flushed": 1}, res)

	assertdb.Query(t, rt.DB, `SELECT count, expires_on::text FROM orgs_tally WHERE org_id = $1 AND scope = 'emails/shared@2025-05-04'`, testdb.Org1.ID).
		Columns(map[string]any{"count": int64(2), "expires_on": "2025-08-02"})
	assertdb.Query(t, rt.DB, `SELECT count, expires_on::text FROM orgs_tally WHERE org_id = $1 AND scope = 'foo'`, testdb.Org1.ID).
		Columns(map[string]any{"count": int64(3), "expires_on": nil})
}
