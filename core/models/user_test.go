package models_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUsers(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshUsers)
	require.NoError(t, err)

	users, err := oa.Users()
	require.NoError(t, err)

	partners := &models.Team{testdb.Partners.ID, testdb.Partners.UUID, "Partners"}

	expectedUsers := []struct {
		id    models.UserID
		uuid  assets.UserUUID
		email string
		name  string
		role  models.UserRole
		team  *models.Team
	}{
		{id: testdb.Admin.ID, uuid: testdb.Admin.UUID, email: "admin1@textit.com", name: "Andy Admin", role: models.UserRoleAdministrator, team: nil},
		{id: testdb.Agent.ID, uuid: testdb.Agent.UUID, email: "agent1@textit.com", name: "Ann D'Agent", role: models.UserRoleAgent, team: partners},
		{id: testdb.Editor.ID, uuid: testdb.Editor.UUID, email: "editor1@textit.com", name: "Ed McEditor", role: models.UserRoleEditor, team: nil},
	}

	require.Len(t, users, len(expectedUsers))

	for i, expected := range expectedUsers {
		assetUser := users[i]
		assert.Equal(t, expected.email, assetUser.Email())
		assert.Equal(t, expected.name, assetUser.Name())

		modelUser := assetUser.(*models.User)
		assert.Equal(t, expected.id, modelUser.ID())
		assert.Equal(t, expected.uuid, modelUser.UUID())
		assert.Equal(t, expected.email, modelUser.Email())
		assert.Equal(t, expected.role, modelUser.Role())
		assert.Equal(t, expected.team, modelUser.Team())

		assert.Equal(t, modelUser, oa.UserByID(expected.id))
		assert.Equal(t, modelUser, oa.UserByUUID(expected.uuid))
	}

	sysID, err := models.GetSystemUserID(ctx, rt.DB.DB)
	assert.NoError(t, err)
	assert.NotEqual(t, sysID, models.NilUserID)

	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org2.ID, models.RefreshUsers)
	require.NoError(t, err)

	users, err = oa.Users()
	require.NoError(t, err)

	require.Len(t, users, 1)
	require.Equal(t, testdb.Org2Admin.UUID, users[0].(*models.User).UUID())
}
