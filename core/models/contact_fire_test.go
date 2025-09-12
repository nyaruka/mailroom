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

func TestContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	testdb.InsertContactFire(rt, testdb.Org1, testdb.Ann, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-5*time.Second), "46aa1e25-9c01-44d7-8223-e43036627505")
	testdb.InsertContactFire(rt, testdb.Org1, testdb.Bob, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-4*time.Second), "531e84a7-d883-40a0-8e7a-b4dde4428ce1")
	testdb.InsertContactFire(rt, testdb.Org2, testdb.Org2Contact, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-3*time.Second), "7c73b6e4-ae33-45a6-9126-be474234b69d")
	testdb.InsertContactFire(rt, testdb.Org2, testdb.Org2Contact, models.ContactFireTypeWaitTimeout, "", time.Now().Add(-2*time.Second), "7c73b6e4-ae33-45a6-9126-be474234b69d")

	remindersEvent1 := oa.CampaignPointByID(testdb.RemindersPoint1.ID)

	err = models.InsertContactFires(ctx, rt.DB, []*models.ContactFire{
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Bob.ID, remindersEvent1, time.Now().Add(2*time.Second)),
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 5)

	// if we add another with same contact+type+scope as an existing.. nothing
	err = models.InsertContactFires(ctx, rt.DB, []*models.ContactFire{
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Bob.ID, remindersEvent1, time.Now().Add(2*time.Second)),
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 5)

	fires, err := models.LoadDueContactfires(ctx, rt, 3)
	assert.NoError(t, err)
	assert.Len(t, fires, 3)
	assert.Equal(t, testdb.Ann.ID, fires[0].ContactID)

	err = models.DeleteContactFires(ctx, rt, []*models.ContactFire{fires[0], fires[1]})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 2)
}

func TestSessionContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertContactFire(rt, testdb.Org1, testdb.Bob, models.ContactFireTypeCampaignPoint, "235", time.Now().Add(2*time.Second), "")

	fires := []*models.ContactFire{
		models.NewFireForSession(testdb.Org1.ID, testdb.Bob.ID, "6ffbe7f4-362b-439c-a253-5e09a1dd4ed6", "d973e18c-009e-4539-80f9-4f7ac60e5f3b", models.ContactFireTypeWaitTimeout, time.Now().Add(time.Minute)),
		models.NewFireForSession(testdb.Org1.ID, testdb.Bob.ID, "6ffbe7f4-362b-439c-a253-5e09a1dd4ed6", "d973e18c-009e-4539-80f9-4f7ac60e5f3b", models.ContactFireTypeWaitExpiration, time.Now().Add(time.Hour)),
		models.NewFireForSession(testdb.Org1.ID, testdb.Bob.ID, "6ffbe7f4-362b-439c-a253-5e09a1dd4ed6", "", models.ContactFireTypeSessionExpiration, time.Now().Add(7*24*time.Hour)),
		models.NewFireForSession(testdb.Org1.ID, testdb.Ann.ID, "736ee995-d246-4ccf-bdde-e9267831da95", "d0ceea41-5b38-4366-82fb-05576e244bd7", models.ContactFireTypeWaitTimeout, time.Now().Add(time.Minute)),
		models.NewFireForSession(testdb.Org1.ID, testdb.Ann.ID, "736ee995-d246-4ccf-bdde-e9267831da95", "d0ceea41-5b38-4366-82fb-05576e244bd7", models.ContactFireTypeWaitExpiration, time.Now().Add(time.Hour)),
		models.NewFireForSession(testdb.Org1.ID, testdb.Ann.ID, "736ee995-d246-4ccf-bdde-e9267831da95", "", models.ContactFireTypeSessionExpiration, time.Now().Add(7*24*time.Hour)),
	}

	err := models.InsertContactFires(ctx, rt.DB, fires)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T' AND session_uuid = '6ffbe7f4-362b-439c-a253-5e09a1dd4ed6'`, testdb.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E' AND session_uuid = '6ffbe7f4-362b-439c-a253-5e09a1dd4ed6'`, testdb.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'S' AND session_uuid = '6ffbe7f4-362b-439c-a253-5e09a1dd4ed6'`, testdb.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T' AND session_uuid = '736ee995-d246-4ccf-bdde-e9267831da95'`, testdb.Ann.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E' AND session_uuid = '736ee995-d246-4ccf-bdde-e9267831da95'`, testdb.Ann.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'S' AND session_uuid = '736ee995-d246-4ccf-bdde-e9267831da95'`, testdb.Ann.ID).Returns(1)

	num, err := models.DeleteSessionFires(ctx, rt.DB, []models.ContactID{testdb.Bob.ID}, true) // all
	assert.NoError(t, err)
	assert.Equal(t, 3, num)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type IN ('T', 'E', 'S')`, testdb.Bob.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`, testdb.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdb.Ann.ID).Returns(3)

	num, err = models.DeleteSessionFires(ctx, rt.DB, []models.ContactID{testdb.Ann.ID}, false) // waits only
	assert.NoError(t, err)
	assert.Equal(t, 2, num)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T'`, testdb.Ann.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E'`, testdb.Ann.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'S'`, testdb.Ann.ID).Returns(1)
}

func TestCampaignContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	remindersEvent1 := oa.CampaignPointByID(testdb.RemindersPoint1.ID)
	remindersEvent2 := oa.CampaignPointByID(testdb.RemindersPoint2.ID)
	remindersEvent3 := oa.CampaignPointByID(testdb.RemindersPoint3.ID)

	testdb.InsertContactFire(rt, testdb.Org1, testdb.Ann, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-4*time.Second), "531e84a7-d883-40a0-8e7a-b4dde4428ce1")

	fires := []*models.ContactFire{
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Bob.ID, remindersEvent1, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Bob.ID, remindersEvent2, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Bob.ID, remindersEvent3, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Ann.ID, remindersEvent1, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Ann.ID, remindersEvent2, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.Ann.ID, remindersEvent3, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.George.ID, remindersEvent1, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.George.ID, remindersEvent2, time.Now()),
		models.NewContactFireForCampaign(testdb.Org1.ID, testdb.George.ID, remindersEvent3, time.Now()),
	}

	err = models.InsertContactFires(ctx, rt.DB, fires)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'E'`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'C'`).Returns(9)

	// test deleting all campaign fires for a contact
	err = models.DeleteAllCampaignFires(ctx, rt.DB, []models.ContactID{testdb.Ann.ID})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'C'`).Returns(6)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdb.Bob.ID).Returns(3)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type IN ('E', 'T')`, testdb.Ann.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`, testdb.Ann.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdb.George.ID).Returns(3)

	// test deleting specific contact/event combinations
	err = models.DeleteCampaignFires(ctx, rt.DB, []*models.FireDelete{
		{ContactID: testdb.Bob.ID, EventID: testdb.RemindersPoint1.ID, FireVersion: 1},
		{ContactID: testdb.George.ID, EventID: testdb.RemindersPoint3.ID, FireVersion: 1},
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'C'`).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdb.Bob.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdb.George.ID).Returns(2)
}
