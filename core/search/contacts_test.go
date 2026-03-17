package search_test

import (
	"bytes"
	"sort"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewContactDoc(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(t, rt)

	mcs, err := models.LoadContacts(ctx, rt.DB, oa, []models.ContactID{testdb.Ann.ID, testdb.Cat.ID})
	require.NoError(t, err)
	require.Len(t, mcs, 2)

	sort.Slice(mcs, func(i, j int) bool { return mcs[i].ID() < mcs[j].ID() })

	// convert to flow contacts
	flowContacts := make(map[models.ContactID]*flows.Contact)
	for _, mc := range mcs {
		fc, err := mc.EngineContact(oa)
		require.NoError(t, err)
		flowContacts[mc.ID()] = fc
	}

	// Ann: has name, status=active, URNs, groups, fields (gender, state, district, ward)
	annFC := flowContacts[testdb.Ann.ID]
	require.NotNil(t, annFC)

	doc := search.NewContactDoc(oa, annFC, testdb.Favorites.ID, []models.FlowID{testdb.Favorites.ID, testdb.PickANumber.ID})

	assert.Equal(t, testdb.Ann.ID, doc.DBID)
	assert.Equal(t, testdb.Org1.ID, doc.OrgID)
	assert.Equal(t, testdb.Ann.UUID, doc.UUID)
	assert.Equal(t, "Ann", doc.Name)
	assert.Equal(t, models.ContactStatusActive, doc.Status)
	assert.NotEmpty(t, doc.CreatedOn)
	assert.Equal(t, testdb.Favorites.ID, doc.FlowID)
	assert.Equal(t, []models.FlowID{testdb.Favorites.ID, testdb.PickANumber.ID}, doc.FlowHistoryIDs)

	// Ann should have URNs
	assert.Len(t, doc.URNs, 1)
	assert.Equal(t, "tel", doc.URNs[0].Scheme)
	assert.Equal(t, "+16055741111", doc.URNs[0].Path)

	// Ann should be in the Doctors group
	assert.Contains(t, doc.GroupIDs, testdb.DoctorsGroup.ID)

	// Ann has no open tickets by default in test fixtures
	assert.Equal(t, 0, doc.Tickets)

	// Ann should have fields: gender, state, district, ward (not age since it's nil)
	fieldsByUUID := make(map[assets.FieldUUID]*search.ContactDocField)
	for _, f := range doc.Fields {
		fieldsByUUID[f.Field] = f
	}

	genderField := fieldsByUUID[testdb.GenderField.UUID]
	require.NotNil(t, genderField, "should have gender field")
	assert.Equal(t, "F", genderField.Text)

	stateField := fieldsByUUID[testdb.StateField.UUID]
	require.NotNil(t, stateField, "should have state field")
	assert.NotEmpty(t, stateField.State)
	assert.NotEmpty(t, stateField.StateKeyword)

	wardField := fieldsByUUID[testdb.WardField.UUID]
	require.NotNil(t, wardField, "should have ward field")
	assert.NotEmpty(t, wardField.Ward)
	assert.NotEmpty(t, wardField.WardKeyword)

	// Cat: has name, status=active, age=30, 1 URN, in Doctors group, no tickets
	catFC := flowContacts[testdb.Cat.ID]
	require.NotNil(t, catFC)

	doc = search.NewContactDoc(oa, catFC, models.NilFlowID, nil)

	assert.Equal(t, testdb.Cat.ID, doc.DBID)
	assert.Equal(t, testdb.Cat.UUID, doc.UUID)
	assert.Equal(t, "Cat", doc.Name)
	assert.Equal(t, models.ContactStatusActive, doc.Status)
	assert.Equal(t, models.NilFlowID, doc.FlowID)
	assert.Nil(t, doc.FlowHistoryIDs)

	assert.Len(t, doc.URNs, 1)
	assert.Equal(t, "tel", doc.URNs[0].Scheme)

	assert.Equal(t, 0, doc.Tickets)

	// Cat should have age field with number
	fieldsByUUID = make(map[assets.FieldUUID]*search.ContactDocField)
	for _, f := range doc.Fields {
		fieldsByUUID[f.Field] = f
	}

	ageField := fieldsByUUID[testdb.AgeField.UUID]
	require.NotNil(t, ageField, "should have age field")
	assert.NotNil(t, ageField.Number)
}

func TestDeindexContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// index all org1 and org2 contacts into the v2 index
	testsuite.IndexOrgContacts(t, rt, testdb.Org1)
	testsuite.IndexOrgContacts(t, rt, testdb.Org2)

	refreshV2 := func() {
		_, err := rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndexV2).Do(ctx)
		require.NoError(t, err)
	}

	refreshV2()

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE org_id = $1`, testdb.Org1.ID).Returns(124)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE org_id = $1`, testdb.Org2.ID).Returns(121)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 124)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	// DeindexContactsByID operates on the v2 index
	deindexedByID, err := search.DeindexContactsByID(ctx, rt, testdb.Org1.ID, []models.ContactID{testdb.Bob.ID, testdb.Cat.ID})
	assert.NoError(t, err)
	assert.Equal(t, 2, deindexedByID)

	refreshV2()

	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 122)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	// DeindexContactsByOrg also operates on the v2 index
	deindexed, err := search.DeindexContactsByOrg(ctx, rt, testdb.Org1.ID, 100)
	assert.NoError(t, err)
	assert.Equal(t, 100, deindexed)

	refreshV2()

	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 22)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	deindexed, err = search.DeindexContactsByOrg(ctx, rt, testdb.Org1.ID, 100)
	assert.NoError(t, err)
	assert.Equal(t, 22, deindexed)

	refreshV2()

	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 0)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	deindexed, err = search.DeindexContactsByOrg(ctx, rt, testdb.Org1.ID, 100)
	assert.NoError(t, err)
	assert.Equal(t, 0, deindexed)
}

func assertSearchCountV2(t *testing.T, rt *runtime.Runtime, query elastic.Query, expected int) {
	src := map[string]any{"query": query}

	resp, err := rt.ES.Client.Count().Index(rt.Config.ElasticContactsIndexV2).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expected, int(resp.Count))
}
