package search_test

import (
	"bytes"
	"sort"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
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

	// convert to engine contacts
	contacts := make(map[models.ContactID]*core.Contact)
	for _, mc := range mcs {
		contact, err := mc.EngineContact(oa)
		require.NoError(t, err)
		contacts[mc.ID()] = contact
	}

	// Ann: has name, status=active, URNs, groups, fields (gender, state, district, ward)
	ann := contacts[testdb.Ann.ID]
	require.NotNil(t, ann)

	doc := search.NewContactDoc(oa, ann, testdb.Favorites.ID, []models.FlowID{testdb.Favorites.ID, testdb.PickANumber.ID})

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
	cat := contacts[testdb.Cat.ID]
	require.NotNil(t, cat)

	doc = search.NewContactDoc(oa, cat, models.NilFlowID, nil)

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

	testsuite.IndexContacts(t, rt)

	refreshV2 := func() {
		_, err := rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(ctx)
		require.NoError(t, err)
	}

	refreshV2()

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE org_id = $1`, testdb.Org1.ID).Returns(124)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE org_id = $1`, testdb.Org2.ID).Returns(121)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 124)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	// deindexing an empty set of UUIDs is a noop
	deindexedByUUID, err := search.DeindexContactsByUUID(ctx, rt, testdb.Org1.ID, []core.ContactUUID{})
	assert.NoError(t, err)
	assert.Equal(t, 0, deindexedByUUID)

	// DeindexContactsByUUID operates on the v3 index
	deindexedByUUID, err = search.DeindexContactsByUUID(ctx, rt, testdb.Org1.ID, []core.ContactUUID{testdb.Bob.UUID, testdb.Cat.UUID})
	assert.NoError(t, err)
	assert.Equal(t, 2, deindexedByUUID)

	refreshV2()

	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 122)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	// DeindexContactsByOrg also operates on the v3 index
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

func TestPruneContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.IndexContacts(t, rt)

	refresh := func() {
		_, err := rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(ctx)
		require.NoError(t, err)
	}

	// release Bob in the database without deindexing him
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, testdb.Bob.ID)

	// and index a doc for a contact which doesn't exist in the database at all
	fakeUUID := core.ContactUUID("826a1421-2d51-45ca-a5a5-b26783d49e2c")
	rt.ES.Writer.Queue(&elastic.Document{
		Index:   rt.Config.ElasticContactsIndex,
		ID:      string(fakeUUID),
		Routing: testdb.Org2.ID.String(),
		Body:    jsonx.MustMarshal(map[string]any{"id": 123456789, "org_id": testdb.Org2.ID}),
	})
	rt.ES.Writer.Flush()
	refresh()

	// default is report only.. no docs are deleted
	counts, err := search.PruneContacts(ctx, rt, false, nil)
	require.NoError(t, err)
	assert.Equal(t, search.PruneCounts{Scanned: 246, Orphaned: 2, Deleted: 0}, counts)

	refresh()
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 124)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 122)

	// with delete flag, orphaned docs are deleted
	var progress []search.PruneCounts
	counts, err = search.PruneContacts(ctx, rt, true, func(c search.PruneCounts) { progress = append(progress, c) })
	require.NoError(t, err)
	assert.Equal(t, search.PruneCounts{Scanned: 246, Orphaned: 2, Deleted: 2}, counts)
	assert.Equal(t, []search.PruneCounts{{Scanned: 246, Orphaned: 2, Deleted: 2}}, progress)

	refresh()
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org1.ID), 123)
	assertSearchCountV2(t, rt, elastic.Term("org_id", testdb.Org2.ID), 121)

	// Bob's doc and the fake doc are gone, other contacts are untouched
	assertSearchCountV2(t, rt, elastic.Ids(string(testdb.Bob.UUID)), 0)
	assertSearchCountV2(t, rt, elastic.Ids(string(fakeUUID)), 0)
	assertSearchCountV2(t, rt, elastic.Ids(string(testdb.Ann.UUID)), 1)

	// re-running finds nothing to prune
	counts, err = search.PruneContacts(ctx, rt, true, nil)
	require.NoError(t, err)
	assert.Equal(t, search.PruneCounts{Scanned: 244, Orphaned: 0, Deleted: 0}, counts)
}

func assertSearchCountV2(t *testing.T, rt *runtime.Runtime, query elastic.Query, expected int) {
	src := map[string]any{"query": query}

	resp, err := rt.ES.Client.Count().Index(rt.Config.ElasticContactsIndex).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expected, int(resp.Count))
}
