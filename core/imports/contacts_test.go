package imports_test

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/imports"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactImports(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	// start with no contacts or URNs
	rt.DB.MustExec(`DELETE FROM contacts_contacturn`)
	rt.DB.MustExec(`DELETE FROM contacts_contactgroup_contacts`)
	rt.DB.MustExec(`DELETE FROM contacts_contact`)
	rt.DB.MustExec(`ALTER SEQUENCE contacts_contact_id_seq RESTART WITH 10000`)
	rt.DB.MustExec(`ALTER SEQUENCE contacts_contacturn_id_seq RESTART WITH 10000`)

	// add contact in other org to make sure we can't update it
	testdb.InsertContact(rt, testdb.Org2, "f7a8016d-69a6-434b-aae7-5142ce4a98ba", "Xavier", "spa", models.ContactStatusActive)

	// add dynamic group to test imported contacts are added to it
	testdb.InsertContactGroup(rt, testdb.Org1, "fc32f928-ad37-477c-a88e-003d30fd7406", "Adults", "age >= 40")

	// give our org a country by setting country on a channel
	rt.DB.MustExec(`UPDATE channels_channel SET country = 'US' WHERE id = $1`, testdb.TwilioChannel.ID)

	testJSON := testsuite.ReadFile("testdata/contacts.json")

	tcs := []struct {
		Description string                `json:"description"`
		Specs       json.RawMessage       `json:"specs"`
		NumCreated  int                   `json:"num_created"`
		NumUpdated  int                   `json:"num_updated"`
		NumErrored  int                   `json:"num_errored"`
		Errors      json.RawMessage       `json:"errors"`
		Contacts    []*models.ContactSpec `json:"contacts"`
	}{}
	jsonx.MustUnmarshal(testJSON, &tcs)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOrg|models.RefreshChannels|models.RefreshGroups)
	require.NoError(t, err)

	uuids.SetGenerator(uuids.NewSeededGenerator(12345, time.Now))
	defer uuids.SetGenerator(uuids.DefaultGenerator)

	for i, tc := range tcs {
		importID := testdb.InsertContactImport(rt, testdb.Org1, testdb.Admin)
		batchID := testdb.InsertContactImportBatch(rt, importID, tc.Specs)

		batch, err := models.LoadContactImportBatch(ctx, rt.DB, batchID)
		require.NoError(t, err)

		err = imports.ImportBatch(ctx, rt, oa, batch, testdb.Admin.ID)
		require.NoError(t, err)

		results := &struct {
			NumCreated int             `db:"num_created"`
			NumUpdated int             `db:"num_updated"`
			NumErrored int             `db:"num_errored"`
			Errors     json.RawMessage `db:"errors"`
		}{}
		err = rt.DB.Get(results, `SELECT num_created, num_updated, num_errored, errors FROM contacts_contactimportbatch WHERE id = $1`, batchID)
		require.NoError(t, err)

		// load all contacts and convert to specs
		contacts := loadAllContacts(t, rt, oa)
		specs := make([]*models.ContactSpec, len(contacts))
		for i, contact := range contacts {
			name := contact.Name()
			lang := string(contact.Language())
			groupUUIDs := make([]assets.GroupUUID, len(contact.Groups().All()))
			for j, group := range contact.Groups().All() {
				groupUUIDs[j] = group.UUID()
			}
			sort.Slice(groupUUIDs, func(i, j int) bool { return strings.Compare(string(groupUUIDs[i]), string(groupUUIDs[j])) < 0 })

			fields := make(map[string]string)
			for key, fv := range contact.Fields() {
				val := types.Render(fv.ToXValue(oa.Env()))
				if val != "" {
					fields[key] = val
				}
			}
			specs[i] = &models.ContactSpec{
				UUID:     contact.UUID(),
				Name:     &name,
				Language: &lang,
				Status:   contact.Status(),
				URNs:     contact.URNs().RawURNs(),
				Fields:   fields,
				Groups:   groupUUIDs,
			}
		}

		actual := tc
		actual.NumCreated = results.NumCreated
		actual.NumUpdated = results.NumUpdated
		actual.NumErrored = results.NumErrored
		actual.Errors = results.Errors
		actual.Contacts = specs

		if !test.UpdateSnapshots {
			assert.Equal(t, tc.NumCreated, actual.NumCreated, "created contacts mismatch in '%s'", tc.Description)
			assert.Equal(t, tc.NumUpdated, actual.NumUpdated, "updated contacts mismatch in '%s'", tc.Description)
			assert.Equal(t, tc.NumErrored, actual.NumErrored, "errored contacts mismatch in '%s'", tc.Description)

			test.AssertEqualJSON(t, tc.Errors, actual.Errors, "errors mismatch in '%s'", tc.Description)

			actualJSON := jsonx.MustMarshal(actual.Contacts)
			expectedJSON := jsonx.MustMarshal(tc.Contacts)
			test.AssertEqualJSON(t, expectedJSON, actualJSON, "imported contacts mismatch in '%s'", tc.Description)
		} else {
			tcs[i] = actual
		}
	}

	if test.UpdateSnapshots {
		testJSON, err = jsonx.MarshalPretty(tcs)
		require.NoError(t, err)

		err = os.WriteFile("testdata/imports.json", testJSON, 0600)
		require.NoError(t, err)
	}
}

func TestLoadContactImport(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa := testdb.Org1.Load(rt)

	importID := testdb.InsertContactImport(rt, testdb.Org1, testdb.Admin)
	batch1ID := testdb.InsertContactImportBatch(rt, importID, []byte(`[
		{"name": "Norbert", "language": "eng", "urns": ["tel:+16055740001"]},
		{"name": "Leah", "urns": ["tel:+16055740002"]}
	]`))
	testdb.InsertContactImportBatch(rt, importID, []byte(`[
		{"name": "Rowan", "language": "spa", "urns": ["tel:+16055740003"]}
	]`))

	imp, err := models.LoadContactImport(ctx, rt.DB, importID)
	require.NoError(t, err)

	assert.Equal(t, testdb.Org1.ID, imp.OrgID)
	assert.Equal(t, testdb.Admin.ID, imp.CreatedByID)
	assert.Equal(t, models.ContactImportStatusProcessing, imp.Status)
	assert.Nil(t, imp.FinishedOn)
	assert.Equal(t, "P", imp.BatchStatuses)

	batch1, err := models.LoadContactImportBatch(ctx, rt.DB, batch1ID)
	require.NoError(t, err)

	assert.Equal(t, importID, batch1.ImportID)
	assert.Equal(t, models.ContactImportStatusPending, batch1.Status)
	assert.NotNil(t, batch1.Specs)
	assert.Equal(t, 0, batch1.RecordStart)
	assert.Equal(t, 2, batch1.RecordEnd)

	err = imports.ImportBatch(ctx, rt, oa, batch1, testdb.Admin.ID)
	require.NoError(t, err)

	imp, err = models.LoadContactImport(ctx, rt.DB, importID)
	require.NoError(t, err)

	batchStatuses := strings.Split(imp.BatchStatuses, "")
	sort.Strings(batchStatuses)
	assert.Equal(t, []string{"C", "P"}, batchStatuses)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactimportbatch WHERE status = 'C' AND finished_on IS NOT NULL`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactimportbatch WHERE status = 'P' AND finished_on IS NULL`).Returns(1)
}

func TestContactSpecUnmarshal(t *testing.T) {
	s := &models.ContactSpec{}
	jsonx.Unmarshal([]byte(`{}`), s)

	assert.Equal(t, flows.ContactUUID(""), s.UUID)
	assert.Nil(t, s.Name)
	assert.Nil(t, s.Language)
	assert.Nil(t, s.URNs)
	assert.Nil(t, s.Fields)
	assert.Nil(t, s.Groups)

	s = &models.ContactSpec{}
	jsonx.Unmarshal([]byte(`{
		"uuid": "8e879527-7e6d-4bff-abc8-b1d41cd4f702", 
		"name": "Bob", 
		"language": "spa",
		"urns": ["tel:+1234567890"],
		"fields": {"age": "39"},
		"groups": ["3972dcc2-6749-4761-a896-7880d6165f2c"]
	}`), s)

	assert.Equal(t, flows.ContactUUID("8e879527-7e6d-4bff-abc8-b1d41cd4f702"), s.UUID)
	assert.Equal(t, "Bob", *s.Name)
	assert.Equal(t, "spa", *s.Language)
	assert.Equal(t, []urns.URN{"tel:+1234567890"}, s.URNs)
	assert.Equal(t, map[string]string{"age": "39"}, s.Fields)
	assert.Equal(t, []assets.GroupUUID{"3972dcc2-6749-4761-a896-7880d6165f2c"}, s.Groups)
}

// utility to load all contacts for the given org and return as slice sorted by ID
func loadAllContacts(t *testing.T, rt *runtime.Runtime, oa *models.OrgAssets) []*flows.Contact {
	rows, err := rt.DB.Query(`SELECT id FROM contacts_contact WHERE org_id = $1`, oa.OrgID())
	require.NoError(t, err)

	var allIDs []models.ContactID

	allIDs, err = dbutil.ScanAllSlice(rows, allIDs)
	require.NoError(t, err)

	contacts, err := models.LoadContacts(context.Background(), rt.DB, oa, allIDs)
	require.NoError(t, err)

	sort.Slice(contacts, func(i, j int) bool { return contacts[i].ID() < contacts[j].ID() })

	flowContacts := make([]*flows.Contact, len(contacts))
	for i := range contacts {
		flowContacts[i], err = contacts[i].EngineContact(oa)
		require.NoError(t, err)
	}

	return flowContacts
}
