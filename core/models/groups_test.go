package models_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGroups(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshOptIns)
	require.NoError(t, err)

	groups, err := oa.Groups()
	require.NoError(t, err)

	tcs := []struct {
		id            models.GroupID
		uuid          assets.GroupUUID
		name          string
		query         string
		expectedCount int
	}{
		{testdata.ActiveGroup.ID, testdata.ActiveGroup.UUID, "Active", "", 134},
		{testdata.ArchivedGroup.ID, testdata.ArchivedGroup.UUID, "Archived", "", 0},
		{testdata.BlockedGroup.ID, testdata.BlockedGroup.UUID, "Blocked", "", 0},
		{testdata.DoctorsGroup.ID, testdata.DoctorsGroup.UUID, "Doctors", "", 121},
		{testdata.OpenTicketsGroup.ID, testdata.OpenTicketsGroup.UUID, "Open Tickets", "tickets > 0", 0},
		{testdata.StoppedGroup.ID, testdata.StoppedGroup.UUID, "Stopped", "", 0},
		{testdata.TestersGroup.ID, testdata.TestersGroup.UUID, "Testers", "", 10},
	}

	assert.Equal(t, 7, len(groups))

	for i, tc := range tcs {
		group := groups[i].(*models.Group)
		assert.Equal(t, tc.uuid, group.UUID())
		assert.Equal(t, tc.id, group.ID())
		assert.Equal(t, tc.name, group.Name())
		assert.Equal(t, tc.query, group.Query())

		count, err := models.GetGroupContactCount(ctx, rt.DB.DB, group.ID())
		assert.NoError(t, err)
		assert.Equal(t, tc.expectedCount, count, "count mismatch for group %s", group.Name())
	}
}
