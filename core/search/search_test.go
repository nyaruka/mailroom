package search_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetContactTotal(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetElastic)

	testsuite.IndexContacts(t, rt)

	// created after indexing so only visible to UUID queries which are resolved from the database
	eve := testdb.InsertContact(t, rt, testdb.Org1, flows.NewContactUUID(), "Eve", i18n.NilLanguage, models.ContactStatusActive)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	tcs := []struct {
		group         *testdb.Group
		query         string
		expectedTotal int64
		expectedError string
	}{
		{group: nil, query: "ann OR bob", expectedTotal: 2},
		{group: testdb.DoctorsGroup, query: "ann OR bob", expectedTotal: 1},
		{group: nil, query: "cat", expectedTotal: 1},
		{group: testdb.ActiveGroup, query: "cat", expectedTotal: 1},
		{group: nil, query: "age >= 30", expectedTotal: 1},
		{group: nil, query: fmt.Sprintf(`uuid = "%s"`, eve.UUID), expectedTotal: 1}, // not yet indexed but resolved from database
		{group: testdb.DoctorsGroup, query: fmt.Sprintf(`uuid = "%s"`, testdb.Ann.UUID), expectedTotal: 1},
		{group: testdb.DoctorsGroup, query: fmt.Sprintf(`uuid = "%s"`, testdb.Bob.UUID), expectedTotal: 0},          // not a doctor
		{group: testdb.BlockedGroup, query: fmt.Sprintf(`uuid = "%s"`, eve.UUID), expectedTotal: 0},                 // not blocked
		{group: nil, query: `uuid = "xyz"`, expectedTotal: 0},                                                       // not a valid UUID so still uses Elastic
		{group: nil, query: fmt.Sprintf(`uuid = "%s" OR uuid = "%s"`, testdb.Ann.UUID, eve.UUID), expectedTotal: 1}, // OR queries still use Elastic so eve not visible
		{group: nil, query: fmt.Sprintf(`uuid = "%s" OR name = "Ann"`, eve.UUID), expectedTotal: 1},                 // as do mixed queries
		{
			group:         nil,
			query:         "goats > 2", // no such contact field
			expectedError: "error parsing query: goats > 2: can't resolve 'goats' to attribute, scheme or field",
		},
	}

	for i, tc := range tcs {
		var group *models.Group
		if tc.group != nil {
			group = oa.GroupByID(tc.group.ID)
		}

		_, total, err := search.GetContactTotal(ctx, rt, oa, group, tc.query)

		if tc.expectedError != "" {
			assert.EqualError(t, err, tc.expectedError)
		} else {
			assert.NoError(t, err, "%d: error encountered performing query", i)
			assert.Equal(t, tc.expectedTotal, total, "%d: total mismatch", i)
		}
	}
}

func TestGetContactUUIDsForQueryPage(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetElastic)

	testsuite.IndexContacts(t, rt)

	// created after indexing so only visible to UUID queries which are resolved from the database
	eve := testdb.InsertContact(t, rt, testdb.Org1, flows.NewContactUUID(), "Eve", i18n.NilLanguage, models.ContactStatusActive)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	tcs := []struct {
		group            *testdb.Group
		excludeUUIDs     []flows.ContactUUID
		query            string
		sort             string
		expectedContacts []flows.ContactUUID
		expectedTotal    int64
		expectedError    string
	}{
		{ // 0
			group:            testdb.ActiveGroup,
			query:            "cat OR bob",
			expectedContacts: []flows.ContactUUID{testdb.Cat.UUID, testdb.Bob.UUID},
			expectedTotal:    2,
		},
		{ // 1
			group:            testdb.BlockedGroup,
			query:            "cat",
			expectedContacts: []flows.ContactUUID{},
			expectedTotal:    0,
		},
		{ // 2
			group:            testdb.ActiveGroup,
			query:            "age >= 30",
			sort:             "-age",
			expectedContacts: []flows.ContactUUID{testdb.Cat.UUID},
			expectedTotal:    1,
		},
		{ // 3
			group:            testdb.ActiveGroup,
			excludeUUIDs:     []flows.ContactUUID{testdb.Cat.UUID},
			query:            "age >= 30",
			sort:             "-age",
			expectedContacts: []flows.ContactUUID{},
			expectedTotal:    0,
		},
		{ // 4
			group:            testdb.ActiveGroup,
			query:            fmt.Sprintf(`uuid = "%s"`, eve.UUID), // not yet indexed but resolved from database
			expectedContacts: []flows.ContactUUID{eve.UUID},
			expectedTotal:    1,
		},
		{ // 5
			group:            testdb.ActiveGroup,
			excludeUUIDs:     []flows.ContactUUID{eve.UUID},
			query:            fmt.Sprintf(`uuid = "%s"`, eve.UUID),
			expectedContacts: []flows.ContactUUID{},
			expectedTotal:    0,
		},
		{ // 6
			group:            testdb.BlockedGroup,
			query:            fmt.Sprintf(`uuid = "%s"`, eve.UUID), // not blocked
			expectedContacts: []flows.ContactUUID{},
			expectedTotal:    0,
		},
		{ // 7
			group:         testdb.BlockedGroup,
			query:         "goats > 2", // no such contact field
			expectedError: "error parsing query: goats > 2: can't resolve 'goats' to attribute, scheme or field",
		},
	}

	for i, tc := range tcs {
		group := oa.GroupByID(tc.group.ID)

		_, uuids, total, err := search.GetContactUUIDsForQueryPage(ctx, rt, oa, group, tc.excludeUUIDs, tc.query, tc.sort, 0, 50)

		if tc.expectedError != "" {
			assert.EqualError(t, err, tc.expectedError)
		} else {
			assert.NoError(t, err, "%d: error encountered performing query", i)
			assert.Equal(t, tc.expectedContacts, uuids, "%d: uuids mismatch", i)
			assert.Equal(t, tc.expectedTotal, total, "%d: total mismatch", i)
		}
	}
}

func TestGetContactUUIDsForQuery(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetElastic)

	oa, err := models.GetOrgAssets(ctx, rt, 1)
	require.NoError(t, err)

	// so that we can test queries that span multiple responses
	cylonUUIDs := make([]flows.ContactUUID, 10003)
	for i := range 10003 {
		cylonUUIDs[i] = testdb.InsertContact(t, rt, testdb.Org1, flows.NewContactUUID(), fmt.Sprintf("Cylon %d", i), i18n.NilLanguage, models.ContactStatusActive).UUID
	}

	// create some extra contacts in the other org to be sure we're filtering correctly
	testdb.InsertContact(t, rt, testdb.Org2, flows.NewContactUUID(), "Cat", i18n.NilLanguage, models.ContactStatusActive)
	testdb.InsertContact(t, rt, testdb.Org2, flows.NewContactUUID(), "Bob", i18n.NilLanguage, models.ContactStatusActive)
	testdb.InsertContact(t, rt, testdb.Org2, flows.NewContactUUID(), "Cylon 0", i18n.NilLanguage, models.ContactStatusActive)

	testsuite.IndexContacts(t, rt)

	// created after indexing so only visible to UUID queries which are resolved from the database
	eve := testdb.InsertContact(t, rt, testdb.Org1, flows.NewContactUUID(), "Eve", i18n.NilLanguage, models.ContactStatusActive)

	tcs := []struct {
		group            *testdb.Group
		status           models.ContactStatus
		query            string
		limit            int
		expectedContacts []flows.ContactUUID
		expectedError    string
	}{
		{
			group:            testdb.ActiveGroup,
			status:           models.NilContactStatus,
			query:            "cat OR bob",
			limit:            -1,
			expectedContacts: []flows.ContactUUID{testdb.Cat.UUID, testdb.Bob.UUID},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "cat OR bob",
			limit:            -1,
			expectedContacts: []flows.ContactUUID{testdb.Cat.UUID, testdb.Bob.UUID},
		},
		{
			group:            testdb.DoctorsGroup,
			status:           models.ContactStatusActive,
			query:            "name = ann",
			limit:            -1,
			expectedContacts: []flows.ContactUUID{testdb.Ann.UUID},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "nobody",
			limit:            -1,
			expectedContacts: []flows.ContactUUID{},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "cat",
			limit:            1,
			expectedContacts: []flows.ContactUUID{testdb.Cat.UUID},
		},
		{
			group:            testdb.DoctorsGroup,
			status:           models.NilContactStatus,
			query:            "",
			limit:            1,
			expectedContacts: []flows.ContactUUID{testdb.Ann.UUID},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "name has cylon",
			limit:            -1,
			expectedContacts: cylonUUIDs,
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            fmt.Sprintf(`uuid = "%s"`, eve.UUID), // not yet indexed but resolved from database
			limit:            -1,
			expectedContacts: []flows.ContactUUID{eve.UUID},
		},
		{
			group:            testdb.DoctorsGroup,
			status:           models.NilContactStatus,
			query:            fmt.Sprintf(`uuid = "%s"`, testdb.Bob.UUID), // not a doctor
			limit:            -1,
			expectedContacts: []flows.ContactUUID{},
		},
		{
			group:            nil,
			status:           models.NilContactStatus,
			query:            fmt.Sprintf(`uuid = "%s" OR uuid = "%s"`, testdb.Cat.UUID, eve.UUID), // OR queries still use Elastic so eve not visible
			limit:            -1,
			expectedContacts: []flows.ContactUUID{testdb.Cat.UUID},
		},
		{
			group:         nil,
			status:        models.ContactStatusActive,
			query:         "goats > 2", // no such contact field
			limit:         -1,
			expectedError: "error parsing query: goats > 2: can't resolve 'goats' to attribute, scheme or field",
		},
	}

	for i, tc := range tcs {
		var group *models.Group
		if tc.group != nil {
			group = oa.GroupByID(tc.group.ID)
		}

		uuids, err := search.GetContactUUIDsForQuery(ctx, rt, oa, group, tc.status, tc.query, tc.limit)

		if tc.expectedError != "" {
			assert.EqualError(t, err, tc.expectedError)
		} else {
			assert.NoError(t, err, "%d: error encountered performing query", i)
			assert.ElementsMatch(t, tc.expectedContacts, uuids, "%d: uuids mismatch", i)
		}
	}
}

func TestGetContactIDsForQuery(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetElastic)

	oa, err := models.GetOrgAssets(ctx, rt, 1)
	require.NoError(t, err)

	// so that we can test queries that span multiple responses
	cylonIDs := make([]models.ContactID, 10003)
	for i := range 10003 {
		cylonIDs[i] = testdb.InsertContact(t, rt, testdb.Org1, flows.NewContactUUID(), fmt.Sprintf("Cylon %d", i), i18n.NilLanguage, models.ContactStatusActive).ID
	}

	// create some extra contacts in the other org to be sure we're filtering correctly
	testdb.InsertContact(t, rt, testdb.Org2, flows.NewContactUUID(), "Cat", i18n.NilLanguage, models.ContactStatusActive)
	testdb.InsertContact(t, rt, testdb.Org2, flows.NewContactUUID(), "Bob", i18n.NilLanguage, models.ContactStatusActive)
	testdb.InsertContact(t, rt, testdb.Org2, flows.NewContactUUID(), "Cylon 0", i18n.NilLanguage, models.ContactStatusActive)

	testsuite.IndexContacts(t, rt)

	tcs := []struct {
		group            *testdb.Group
		status           models.ContactStatus
		query            string
		limit            int
		expectedContacts []models.ContactID
		expectedError    string
	}{
		{
			group:            testdb.ActiveGroup,
			status:           models.NilContactStatus,
			query:            "cat OR bob",
			limit:            -1,
			expectedContacts: []models.ContactID{testdb.Cat.ID, testdb.Bob.ID},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "cat OR bob",
			limit:            -1,
			expectedContacts: []models.ContactID{testdb.Cat.ID, testdb.Bob.ID},
		},
		{
			group:            testdb.DoctorsGroup,
			status:           models.ContactStatusActive,
			query:            "name = ann",
			limit:            -1,
			expectedContacts: []models.ContactID{testdb.Ann.ID},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "nobody",
			limit:            -1,
			expectedContacts: []models.ContactID{},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "cat",
			limit:            1,
			expectedContacts: []models.ContactID{testdb.Cat.ID},
		},
		{
			group:            testdb.DoctorsGroup,
			status:           models.NilContactStatus,
			query:            "",
			limit:            1,
			expectedContacts: []models.ContactID{testdb.Ann.ID},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "name has cylon",
			limit:            -1,
			expectedContacts: cylonIDs,
		},
		{
			group:         nil,
			status:        models.ContactStatusActive,
			query:         "goats > 2", // no such contact field
			limit:         -1,
			expectedError: "error parsing query: goats > 2: can't resolve 'goats' to attribute, scheme or field",
		},
	}

	for i, tc := range tcs {
		var group *models.Group
		if tc.group != nil {
			group = oa.GroupByID(tc.group.ID)
		}

		ids, err := search.GetContactIDsForQuery(ctx, rt, oa, group, tc.status, tc.query, tc.limit)

		if tc.expectedError != "" {
			assert.EqualError(t, err, tc.expectedError)
		} else {
			assert.NoError(t, err, "%d: error encountered performing query", i)
			assert.ElementsMatch(t, tc.expectedContacts, ids, "%d: ids mismatch", i)
		}
	}
}
