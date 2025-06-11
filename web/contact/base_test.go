package contact_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/clocks"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/web/contact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreate(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	// detach Cathy's tel URN
	rt.DB.MustExec(`UPDATE contacts_contacturn SET contact_id = NULL WHERE contact_id = $1`, testdb.Cathy.ID)

	rt.DB.MustExec(`ALTER SEQUENCE contacts_contact_id_seq RESTART WITH 30000`)

	testsuite.RunWebTests(t, ctx, rt, "testdata/create.json", nil)
}

func TestDeindex(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetElastic)

	testsuite.RunWebTests(t, ctx, rt, "testdata/deindex.json", nil)
}

func TestExport(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/export.json", nil)
}

func TestExportPreview(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/export_preview.json", nil)
}

func TestInspect(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	// give cathy an unsendable twitterid URN with a display value
	testdb.InsertContactURN(rt, testdb.Org1, testdb.Cathy, urns.URN("twitterid:23145325#cathy"), 20000, nil)

	testsuite.RunWebTests(t, ctx, rt, "testdata/inspect.json", nil)
}

func TestModify(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	oa := testdb.Org1.Load(rt)

	// to be deterministic, update the creation date on cathy
	rt.DB.MustExec(`UPDATE contacts_contact SET created_on = $1 WHERE id = $2`, time.Date(2018, 7, 6, 12, 30, 0, 123456789, time.UTC), testdb.Cathy.ID)

	// make our campaign group dynamic
	rt.DB.MustExec(`UPDATE contacts_contactgroup SET query = 'age > 18' WHERE id = $1`, testdb.DoctorsGroup.ID)

	// insert an event on our campaign that is based on created on
	testdb.InsertCampaignFlowEvent(rt, testdb.RemindersCampaign, testdb.Favorites, testdb.CreatedOnField, 1000, "W")

	// for simpler tests we clear out cathy's fields and groups to start
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = NULL WHERE id = $1`, testdb.Cathy.ID)
	rt.DB.MustExec(`DELETE FROM contacts_contactgroup_contacts WHERE contact_id = $1`, testdb.Cathy.ID)
	rt.DB.MustExec(`UPDATE contacts_contacturn SET contact_id = NULL WHERE contact_id = $1`, testdb.Cathy.ID)

	// because we made changes to a group above, need to make sure we don't use stale org assets
	models.FlushCache()

	// lock a contact to test skipping them
	clocks.TryToLock(ctx, rt, oa, []models.ContactID{testdb.Alexandra.ID}, time.Second)

	testsuite.RunWebTests(t, ctx, rt, "testdata/modify.json", nil)
}

func TestInterrupt(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	// give Cathy a completed and a waiting session
	testdb.InsertFlowSession(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, models.NilCallID)
	testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Cathy, models.FlowTypeMessaging, testdb.Favorites, models.NilCallID)

	// give Bob a waiting session
	testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Bob, models.FlowTypeMessaging, testdb.PickANumber, models.NilCallID)

	testsuite.RunWebTests(t, ctx, rt, "testdata/interrupt.json", nil)
}

func TestParseQuery(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	testsuite.RunWebTests(t, ctx, rt, "testdata/parse_query.json", nil)
}

func TestSearch(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/search.json", nil)
}

func TestURNs(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/urns.json", nil)
}

func TestSpecToCreation(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	sa := oa.SessionAssets()
	env := envs.NewBuilder().Build()

	// empty spec is valid
	s := &models.ContactSpec{}
	c, err := contact.SpecToCreation(s, env, sa)
	assert.NoError(t, err)
	assert.Equal(t, "", c.Name)
	assert.Equal(t, i18n.NilLanguage, c.Language)
	assert.Equal(t, 0, len(c.URNs))
	assert.Equal(t, 0, len(c.Mods))

	// try to set invalid language
	lang := "xyzd"
	s = &models.ContactSpec{Language: &lang}
	_, err = contact.SpecToCreation(s, env, sa)
	assert.EqualError(t, err, "invalid language: iso-639-3 codes must be 3 characters, got: xyzd")

	// try to set non-existent contact field
	s = &models.ContactSpec{Fields: map[string]string{"goats": "7"}}
	_, err = contact.SpecToCreation(s, env, sa)
	assert.EqualError(t, err, "unknown contact field 'goats'")

	// try to add to non-existent group
	s = &models.ContactSpec{Groups: []assets.GroupUUID{"52f6c50e-f9a8-4f24-bb80-5c9f144ed27f"}}
	_, err = contact.SpecToCreation(s, env, sa)
	assert.EqualError(t, err, "unknown contact group '52f6c50e-f9a8-4f24-bb80-5c9f144ed27f'")

	// try to add to dynamic group
	s = &models.ContactSpec{Groups: []assets.GroupUUID{"52f6c50e-f9a8-4f24-bb80-5c9f144ed27f"}}
	_, err = contact.SpecToCreation(s, env, sa)
	assert.EqualError(t, err, "unknown contact group '52f6c50e-f9a8-4f24-bb80-5c9f144ed27f'")
}
