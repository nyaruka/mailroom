package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdata.InsertContactFire(rt, testdata.Org1, testdata.Cathy, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-5*time.Second), "46aa1e25-9c01-44d7-8223-e43036627505", map[string]any{"call_id": 1234})
	testdata.InsertContactFire(rt, testdata.Org1, testdata.Bob, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-4*time.Second), "531e84a7-d883-40a0-8e7a-b4dde4428ce1", map[string]any{})
	testdata.InsertContactFire(rt, testdata.Org2, testdata.Org2Contact, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-3*time.Second), "7c73b6e4-ae33-45a6-9126-be474234b69d", map[string]any{})
	testdata.InsertContactFire(rt, testdata.Org2, testdata.Org2Contact, models.ContactFireTypeWaitTimeout, "", time.Now().Add(-2*time.Second), "7c73b6e4-ae33-45a6-9126-be474234b69d", map[string]any{})

	err := models.InsertContactFires(ctx, rt.DB, []*models.ContactFire{
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, testdata.RemindersEvent1.ID, time.Now().Add(2*time.Second)),
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 5)

	// if we add another with same contact+type+scope as an existing.. nothing
	err = models.InsertContactFires(ctx, rt.DB, []*models.ContactFire{
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, testdata.RemindersEvent1.ID, time.Now().Add(2*time.Second)),
	})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 5)

	fires, err := models.LoadDueContactfires(ctx, rt, 3)
	assert.NoError(t, err)
	assert.Len(t, fires, 3)
	assert.Equal(t, testdata.Cathy.ID, fires[0].ContactID)
	assert.Equal(t, models.CallID(1234), fires[0].Extra.V.CallID)

	err = models.DeleteContactFires(ctx, rt, []*models.ContactFire{fires[0], fires[1]})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`, 2)
}

func TestSessionContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdata.InsertContactFire(rt, testdata.Org1, testdata.Bob, models.ContactFireTypeCampaign, "235", time.Now().Add(2*time.Second), "", map[string]any{})

	testFlows := testdata.ImportFlows(rt, testdata.Org1, "testdata/session_test_flows.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	modelContact1, _, _ := testdata.Bob.Load(rt, oa)
	modelContact2, _, _ := testdata.Cathy.Load(rt, oa)

	_, flowSession1, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact(testdata.Bob.UUID, flows.ContactID(testdata.Bob.ID), "Bob", "eng", "").MustBuild()
	_, flowSession2, sprint2 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact(testdata.Cathy.UUID, flows.ContactID(testdata.Cathy.ID), "Cathy", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	modelSessions, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession1, flowSession2}, []flows.Sprint{sprint1, sprint2}, []*models.Contact{modelContact1, modelContact2}, nil, models.NilStartID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E' AND session_uuid = $2`, testdata.Bob.ID, modelSessions[0].UUID()).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T' AND session_uuid = $2`, testdata.Bob.ID, modelSessions[0].UUID()).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E' AND session_uuid = $2`, testdata.Cathy.ID, modelSessions[1].UUID()).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T' AND session_uuid = $2`, testdata.Cathy.ID, modelSessions[1].UUID()).Returns(1)

	num, err := models.DeleteSessionContactFires(ctx, rt.DB, []models.ContactID{testdata.Bob.ID})
	assert.NoError(t, err)
	assert.Equal(t, 2, num)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type IN ('E', 'T')`, testdata.Bob.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.Cathy.ID).Returns(2)
}

func TestCampaignContactFires(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdata.InsertContactFire(rt, testdata.Org1, testdata.Cathy, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-4*time.Second), "531e84a7-d883-40a0-8e7a-b4dde4428ce1", map[string]any{})

	fires := []*models.ContactFire{
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, testdata.RemindersEvent1.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, testdata.RemindersEvent2.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Bob.ID, testdata.RemindersEvent3.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Cathy.ID, testdata.RemindersEvent1.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Cathy.ID, testdata.RemindersEvent2.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.Cathy.ID, testdata.RemindersEvent3.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.George.ID, testdata.RemindersEvent1.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.George.ID, testdata.RemindersEvent2.ID, time.Now()),
		models.NewContactFireForCampaign(testdata.Org1.ID, testdata.George.ID, testdata.RemindersEvent3.ID, time.Now()),
	}

	err := models.InsertContactFires(ctx, rt.DB, fires)
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
	err = models.DeleteCampaignContactFires(ctx, rt.DB, []*models.FireDelete{{testdata.Bob.ID, testdata.RemindersEvent1.ID}, {testdata.George.ID, testdata.RemindersEvent3.ID}})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE fire_type = 'C'`).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.Bob.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire WHERE contact_id = $1`, testdata.George.ID).Returns(2)
}
