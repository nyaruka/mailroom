package models_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGroups(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOptIns)
	require.NoError(t, err)

	groups, err := oa.Groups()
	require.NoError(t, err)
	assert.Len(t, groups, 3) // excludes the status groups
	assert.Equal(t, testdb.DoctorsGroup.UUID, groups[0].UUID())
	assert.Equal(t, "Doctors", groups[0].Name())

	tcs := []struct {
		group         *testdb.Group
		name          string
		query         string
		expectedCount int
	}{
		{testdb.ActiveGroup, "\\Active", "", 124},
		{testdb.BlockedGroup, "\\Blocked", "", 0},
		{testdb.DoctorsGroup, "Doctors", "", 121},
		{testdb.OpenTicketsGroup, "Open Tickets", "tickets > 0", 0},
	}

	for _, tc := range tcs {
		group := oa.GroupByUUID(tc.group.UUID)
		assert.Equal(t, tc.group.UUID, group.UUID())
		assert.Equal(t, tc.group.ID, group.ID())
		assert.Equal(t, tc.name, group.Name())
		assert.Equal(t, tc.query, group.Query())

		count, err := models.GetGroupContactCount(ctx, rt.DB.DB, group.ID())
		assert.NoError(t, err)
		assert.Equal(t, tc.expectedCount, count, "count mismatch for group %s", group.Name())
	}
}
