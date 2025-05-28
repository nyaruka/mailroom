package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdata.Org1.ID)
	require.NoError(t, err)

	testdata.InsertContactFire(rt, testdata.Org1, testdata.Cathy, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-5*time.Second), "46aa1e25-9c01-44d7-8223-e43036627505")
	testdata.InsertContactFire(rt, testdata.Org1, testdata.Bob, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-4*time.Second), "531e84a7-d883-40a0-8e7a-b4dde4428ce1")
	testdata.InsertContactFire(rt, testdata.Org2, testdata.Org2Contact, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-3*time.Second), "7c73b6e4-ae33-45a6-9126-be474234b69d")
	testdata.InsertContactFire(rt, testdata.Org2, testdata.Org2Contact, models.ContactFireTypeWaitTimeout, "", time.Now().Add(-2*time.Second), "7c73b6e4-ae33-45a6-9126-be474234b69d")

	remindersEvent1 := oa.CampaignEventByID(testdata.RemindersEvent1.ID)

	err = models.InsertContactFires(ctx, rt.DB, []*models.ContactFire{
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, remindersEvent1, time.Now().Add(2*time.Second)),
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 5)

	// if we add another with same contact+type+scope as an existing.. nothing
	err = models.InsertContactFires(ctx, rt.DB, []*models.ContactFire{
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, remindersEvent1, time.Now().Add(2*time.Second)),
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 5)

	fires, err := models.LoadDueContactfires(ctx, rt, 3)
	assert.NoError(t, err)
	assert.Len(t, fires, 3)
	assert.Equal(t, testdata.Cathy.ID, fires[0].ContactID)

	err = models.DeleteContactFires(ctx, rt, []*models.ContactFire{fires[0], fires[1]})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 2)
}

func TestSessionContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdata.InsertContactFire(rt, testdata.Org1, testdata.Bob, models.ContactFireTypeCampaignEvent, "235", time.Now().Add(2*time.Second), "")

	fires := []*models.ContactFire{
		models.NewFireForSession(testdata.Org1.ID, testdata.Bob.ID, "6ffbe7f4-362b-439c-a253-5e09a1dd4ed6", "d973e18c-009e-4539-80f9-4f7ac60e5f3b", models.ContactFireTypeWaitTimeout, time.Now().Add(time.Minute)),
		models.NewFireForSession(testdata.Org1.ID, testdata.Bob.ID, "6ffbe7f4-362b-439c-a253-5e09a1dd4ed6", "d973e18c-009e-4539-80f9-4f7ac60e5f3b", models.ContactFireTypeWaitExpiration, time.Now().Add(time.Hour)),
		models.NewFireForSession(testdata.Org1.ID, testdata.Bob.ID, "6ffbe7f4-362b-439c-a253-5e09a1dd4ed6", "", models.ContactFireTypeSessionExpiration, time.Now().Add(7*24*time.Hour)),
		models.NewFireForSession(testdata.Org1.ID, testdata.Cathy.ID, "736ee995-d246-4ccf-bdde-e9267831da95", "d0ceea41-5b38-4366-82fb-05576e244bd7", models.ContactFireTypeWaitTimeout, time.Now().Add(time.Minute)),
		models.NewFireForSession(testdata.Org1.ID, testdata.Cathy.ID, "736ee995-d246-4ccf-bdde-e9267831da95", "d0ceea41-5b38-4366-82fb-05576e244bd7", models.ContactFireTypeWaitExpiration, time.Now().Add(time.Hour)),
		models.NewFireForSession(testdata.Org1.ID, testdata.Cathy.ID, "736ee995-d246-4ccf-bdde-e9267831da95", "", models.ContactFireTypeSessionExpiration, time.Now().Add(7*24*time.Hour)),
	}

	err := models.InsertContactFires(ctx, rt.DB, fires)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T' AND session_uuid = '6ffbe7f4-362b-439c-a253-5e09a1dd4ed6'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E' AND session_uuid = '6ffbe7f4-362b-439c-a253-5e09a1dd4ed6'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'S' AND session_uuid = '6ffbe7f4-362b-439c-a253-5e09a1dd4ed6'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T' AND session_uuid = '736ee995-d246-4ccf-bdde-e9267831da95'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E' AND session_uuid = '736ee995-d246-4ccf-bdde-e9267831da95'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'S' AND session_uuid = '736ee995-d246-4ccf-bdde-e9267831da95'`, testdata.Cathy.ID).Returns(1)

	num, err := models.DeleteSessionContactFires(ctx, rt.DB, []models.ContactID{testdata.Bob.ID}, true) // all
	assert.NoError(t, err)
	assert.Equal(t, 3, num)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type IN ('T', 'E', 'S')`, testdata.Bob.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.Cathy.ID).Returns(3)

	num, err = models.DeleteSessionContactFires(ctx, rt.DB, []models.ContactID{testdata.Cathy.ID}, false) // waits only
	assert.NoError(t, err)
	assert.Equal(t, 2, num)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T'`, testdata.Cathy.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E'`, testdata.Cathy.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'S'`, testdata.Cathy.ID).Returns(1)
}

func TestCampaignContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdata.Org1.ID)
	require.NoError(t, err)

	remindersEvent1 := oa.CampaignEventByID(testdata.RemindersEvent1.ID)
	remindersEvent2 := oa.CampaignEventByID(testdata.RemindersEvent2.ID)
	remindersEvent3 := oa.CampaignEventByID(testdata.RemindersEvent3.ID)

	testdata.InsertContactFire(rt, testdata.Org1, testdata.Cathy, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-4*time.Second), "531e84a7-d883-40a0-8e7a-b4dde4428ce1")

	fires := []*models.ContactFire{
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, remindersEvent1, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, remindersEvent2, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, remindersEvent3, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Cathy.ID, remindersEvent1, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Cathy.ID, remindersEvent2, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Cathy.ID, remindersEvent3, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.George.ID, remindersEvent1, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.George.ID, remindersEvent2, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.George.ID, remindersEvent3, time.Now()),
	}

	err = models.InsertContactFires(ctx, rt.DB, fires)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'E'`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'C'`).Returns(9)

	// test deleting all campaign fires for a contact
	err = models.DeleteAllCampaignContactFires(ctx, rt.DB, []models.ContactID{testdata.Cathy.ID})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'C'`).Returns(6)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.Bob.ID).Returns(3)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type IN ('E', 'T')`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`, testdata.Cathy.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.George.ID).Returns(3)

	// test deleting specific contact/event combinations
	err = models.DeleteCampaignContactFires(ctx, rt.DB, []*models.FireDelete{
		{ContactID: testdata.Bob.ID, EventID: testdata.RemindersEvent1.ID, FireVersion: 1},
		{ContactID: testdata.George.ID, EventID: testdata.RemindersEvent3.ID, FireVersion: 1},
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'C'`).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.Bob.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.George.ID).Returns(2)
}
