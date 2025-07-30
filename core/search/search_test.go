package search_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetContactTotal(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	tcs := []struct {
		group         *testdb.Group
		query         string
		expectedTotal int64
		expectedError string
	}{
		{group: nil, query: "cathy OR bob", expectedTotal: 2},
		{group: testdb.DoctorsGroup, query: "cathy OR bob", expectedTotal: 1},
		{group: nil, query: "george", expectedTotal: 1},
		{group: testdb.ActiveGroup, query: "george", expectedTotal: 1},
		{group: nil, query: "age >= 30", expectedTotal: 1},
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

func TestGetContactIDsForQueryPage(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	tcs := []struct {
		group            *testdb.Group
		excludeIDs       []models.ContactID
		query            string
		sort             string
		expectedContacts []models.ContactID
		expectedTotal    int64
		expectedError    string
	}{
		{ // 0
			group:            testdb.ActiveGroup,
			query:            "george OR bob",
			expectedContacts: []models.ContactID{testdb.George.ID, testdb.Bob.ID},
			expectedTotal:    2,
		},
		{ // 1
			group:            testdb.BlockedGroup,
			query:            "george",
			expectedContacts: []models.ContactID{},
			expectedTotal:    0,
		},
		{ // 2
			group:            testdb.ActiveGroup,
			query:            "age >= 30",
			sort:             "-age",
			expectedContacts: []models.ContactID{testdb.George.ID},
			expectedTotal:    1,
		},
		{ // 3
			group:            testdb.ActiveGroup,
			excludeIDs:       []models.ContactID{testdb.George.ID},
			query:            "age >= 30",
			sort:             "-age",
			expectedContacts: []models.ContactID{},
			expectedTotal:    0,
		},
		{ // 4
			group:         testdb.BlockedGroup,
			query:         "goats > 2", // no such contact field
			expectedError: "error parsing query: goats > 2: can't resolve 'goats' to attribute, scheme or field",
		},
	}

	for i, tc := range tcs {
		group := oa.GroupByID(tc.group.ID)

		_, ids, total, err := search.GetContactIDsForQueryPage(ctx, rt, oa, group, tc.excludeIDs, tc.query, tc.sort, 0, 50)

		if tc.expectedError != "" {
			assert.EqualError(t, err, tc.expectedError)
		} else {
			assert.NoError(t, err, "%d: error encountered performing query", i)
			assert.Equal(t, tc.expectedContacts, ids, "%d: ids mismatch", i)
			assert.Equal(t, tc.expectedTotal, total, "%d: total mismatch", i)
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
		cylonIDs[i] = testdb.InsertContact(rt, testdb.Org1, flows.NewContactUUID(), fmt.Sprintf("Cylon %d", i), i18n.NilLanguage, models.ContactStatusActive).ID
	}

	// create some extra contacts in the other org to be sure we're filtering correctly
	testdb.InsertContact(rt, testdb.Org2, flows.NewContactUUID(), "George", i18n.NilLanguage, models.ContactStatusActive)
	testdb.InsertContact(rt, testdb.Org2, flows.NewContactUUID(), "Bob", i18n.NilLanguage, models.ContactStatusActive)
	testdb.InsertContact(rt, testdb.Org2, flows.NewContactUUID(), "Cylon 0", i18n.NilLanguage, models.ContactStatusActive)

	testsuite.ReindexElastic(t, rt)

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
			query:            "george OR bob",
			limit:            -1,
			expectedContacts: []models.ContactID{testdb.George.ID, testdb.Bob.ID},
		},
		{
			group:            nil,
			status:           models.ContactStatusActive,
			query:            "george OR bob",
			limit:            -1,
			expectedContacts: []models.ContactID{testdb.George.ID, testdb.Bob.ID},
		},
		{
			group:            testdb.DoctorsGroup,
			status:           models.ContactStatusActive,
			query:            "name = cathy",
			limit:            -1,
			expectedContacts: []models.ContactID{testdb.Cathy.ID},
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
			query:            "george",
			limit:            1,
			expectedContacts: []models.ContactID{testdb.George.ID},
		},
		{
			group:            testdb.DoctorsGroup,
			status:           models.NilContactStatus,
			query:            "",
			limit:            1,
			expectedContacts: []models.ContactID{testdb.Cathy.ID},
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
