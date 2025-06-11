package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDailyCounts(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	err = models.InsertDailyCounts(ctx, rt.DB, oa, time.Date(2025, 4, 10, 13, 14, 30, 0, time.UTC), map[string]int{"foo": 1, "bar": 2})
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM orgs_dailycount`).Returns(2)

	err = models.InsertDailyCounts(ctx, rt.DB, oa, time.Date(2025, 4, 10, 13, 14, 30, 0, time.UTC), map[string]int{"foo": 3})
	assert.NoError(t, err)

	err = models.InsertDailyCounts(ctx, rt.DB, oa, time.Date(2025, 4, 11, 13, 14, 30, 0, time.UTC), map[string]int{"foo": 5})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM orgs_dailycount`).Returns(4)
	testsuite.AssertDailyCounts(t, rt, testdb.Org1, map[string]int{
		"2025-04-10/foo": 4,
		"2025-04-10/bar": 2,
		"2025-04-11/foo": 5,
	})
}
