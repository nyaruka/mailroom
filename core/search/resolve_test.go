package search_test

import (
	"bytes"
	"testing"

	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRecipients(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	group1 := testdb.InsertContactGroup(t, rt, testdb.Org1, "a85acec9-3895-4ffd-87c1-c69a25781a85", "Group 1", "", testdb.Cat, testdb.Dan)
	group2 := testdb.InsertContactGroup(t, rt, testdb.Org1, "eb578345-595e-4e36-a68b-6941e242cdbb", "Group 2", "", testdb.Cat)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshGroups)
	require.NoError(t, err)

	tcs := []struct {
		flow               *testdb.Flow
		recipients         *search.Recipients
		limit              int
		expectedIDs        []models.ContactID
		expectedCreatedIDs []models.ContactID
	}{
		{ // 0 nobody
			recipients:  &search.Recipients{},
			expectedIDs: []models.ContactID{},
		},
		{ // 1 only explicit contacts
			recipients: &search.Recipients{
				ContactIDs: []models.ContactID{testdb.Bob.ID, testdb.Dan.ID},
			},
			limit:       -1,
			expectedIDs: []models.ContactID{testdb.Bob.ID, testdb.Dan.ID},
		},
		{ // 2 explicit contacts, group and query
			recipients: &search.Recipients{
				ContactIDs: []models.ContactID{testdb.Bob.ID},
				GroupIDs:   []models.GroupID{group1.ID},
				Query:      `name = "Ann" OR name = "Bob"`,
			},
			limit:       -1,
			expectedIDs: []models.ContactID{testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID, testdb.Ann.ID},
		},
		{ // 3 exclude group
			recipients: &search.Recipients{
				ContactIDs:      []models.ContactID{testdb.Cat.ID, testdb.Bob.ID},
				ExcludeGroupIDs: []models.GroupID{group2.ID},
			},
			limit:       -1,
			expectedIDs: []models.ContactID{testdb.Bob.ID},
		},
		{ // 4 limit number returned
			recipients: &search.Recipients{
				Query: `name = "Ann" OR name = "Bob"`,
			},
			limit:       1,
			expectedIDs: []models.ContactID{testdb.Ann.ID},
		},
		{ // 5 create new contacts from URNs
			recipients: &search.Recipients{
				ContactIDs: []models.ContactID{testdb.Bob.ID},
				URNs:       []urns.URN{"tel:+1234000001", "tel:+1234000002"},
				Exclusions: models.Exclusions{InAFlow: true},
			},
			limit:              -1,
			expectedIDs:        []models.ContactID{testdb.Bob.ID, 30000, 30001},
			expectedCreatedIDs: []models.ContactID{30000, 30001},
		},
		{ // 6 new contacts not included if excluding based on last seen
			recipients: &search.Recipients{
				URNs:       []urns.URN{"tel:+1234000003"},
				Exclusions: models.Exclusions{NotSeenSinceDays: 10},
			},
			limit:       -1,
			expectedIDs: []models.ContactID{},
		},
		{ // 7 new contacts is now an existing contact that can be searched
			recipients: &search.Recipients{
				URNs: []urns.URN{"tel:+1234000003"},
			},
			limit:       -1,
			expectedIDs: []models.ContactID{30002},
		},
	}

	for i, tc := range tcs {
		testsuite.IndexContacts(t, rt)

		var flow *models.Flow
		if tc.flow != nil {
			flow = tc.flow.Load(t, rt, oa)
		}

		actualIDs, actualCreatedIDs, err := search.ResolveRecipients(ctx, rt, oa, testdb.Admin.ID, flow, tc.recipients, tc.limit)
		assert.NoError(t, err)
		assert.ElementsMatch(t, tc.expectedIDs, actualIDs, "contact ids mismatch in %d", i)
		assert.ElementsMatch(t, tc.expectedCreatedIDs, actualCreatedIDs, "created contact ids mismatch in %d", i)
	}
}

func TestResolveRecipientsIndexesExcludedCreatedContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	// resolve a raw URN with a last seen exclusion which the created contact can't satisfy
	matches, createdIDs, err := search.ResolveRecipients(ctx, rt, oa, testdb.Admin.ID, nil, &search.Recipients{
		URNs:       []urns.URN{"tel:+1234000099"},
		Exclusions: models.Exclusions{NotSeenSinceDays: 10},
	}, -1)
	require.NoError(t, err)
	assert.Empty(t, matches)
	assert.Empty(t, createdIDs)

	// contact is created but excluded, so it should have been indexed directly
	rt.ES.Writer.Flush()
	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(ctx)
	require.NoError(t, err)

	src := map[string]any{"query": elastic.Nested("urns", elastic.Term("urns.path.keyword", "+1234000099"))}
	resp, err := rt.ES.Client.Count().Index(rt.Config.ElasticContactsIndex).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Count, "expected excluded created contact to be indexed")
}
