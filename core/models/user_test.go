package models_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUsers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

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

	// create a global admin user who isn't a member of any org
	var globalAdminID models.UserID
	rt.DB.MustExec(`INSERT INTO auth_group(name) VALUES('Global Administrators')`)
	err = rt.DB.Get(&globalAdminID, `INSERT INTO users_user(
		password, is_superuser, uuid, first_name, last_name, email, language, date_joined, is_system, is_staff, is_active, settings
	) VALUES(
		'', FALSE, 'b2b1f7a8-9a1c-4e2f-8d3a-5c6e7f8a9b0c', 'Gloria', 'Global', 'gloria@textit.com', 'en-us', NOW(), FALSE, FALSE, TRUE, '{}'
	) RETURNING id`)
	require.NoError(t, err)
	rt.DB.MustExec(
		`INSERT INTO users_user_groups(user_id, group_id) SELECT $1, id FROM auth_group WHERE name = 'Global Administrators'`,
		globalAdminID,
	)

	// they should appear as an administrator in org 2 despite having no membership
	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org2.ID, models.RefreshUsers)
	require.NoError(t, err)

	users, err = oa.Users()
	require.NoError(t, err)
	require.Len(t, users, 2)

	globalAdmin := oa.UserByID(globalAdminID)
	require.NotNil(t, globalAdmin)
	assert.Equal(t, "gloria@textit.com", globalAdmin.Email())
	assert.Equal(t, models.UserRoleAdministrator, globalAdmin.Role())
	assert.Nil(t, globalAdmin.Team())

	// but an explicit membership takes precedence
	rt.DB.MustExec(
		`INSERT INTO orgs_orgmembership(org_id, user_id, role_code, can_assign, can_reply_non_own) VALUES($1, $2, 'T', TRUE, TRUE)`,
		testdb.Org1.ID, globalAdminID,
	)

	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshUsers)
	require.NoError(t, err)

	users, err = oa.Users()
	require.NoError(t, err)
	require.Len(t, users, 4)

	globalAdmin = oa.UserByID(globalAdminID)
	require.NotNil(t, globalAdmin)
	assert.Equal(t, models.UserRoleAgent, globalAdmin.Role())
}
