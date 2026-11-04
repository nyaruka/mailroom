package crons_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
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
	require.NoError(t, rt.Tallies.Increment(ctx, vc, map[string]int64{
		models.TallyField(testdb.Org1.ID, "emails/shared", day): 2,
		models.TallyField(testdb.Org1.ID, "foo", time.Time{}):   3,
	}))

	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"flushed": 2}, res)

	assertdb.Query(t, rt.DB, `SELECT scope, count FROM orgs_tally WHERE org_id = $1`, testdb.Org1.ID).
		Map(map[string]any{"emails/shared@2025-05-04": int64(2), "foo": int64(3)})
	assertdb.Query(t, rt.DB, `SELECT last_batch FROM orgs_tallyflush WHERE name = 'tallies'`).Returns(int64(1))
}
