package search_test

import (
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestSmartGroups(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	// insert an event on our campaign
	newEvent := testdb.InsertCampaignFlowEvent(rt, testdb.RemindersCampaign, testdb.Favorites, testdb.JoinedField, 1000, "W")

	// clear Cathy's value
	rt.DB.MustExec(`update contacts_contact set fields = fields - $2 WHERE id = $1`, testdb.Cathy.ID, testdb.JoinedField.UUID)

	// and populate Bob's
	rt.DB.MustExec(
		fmt.Sprintf(`update contacts_contact set fields = fields || '{"%s": { "text": "2029-09-15T12:00:00+00:00", "datetime": "2029-09-15T12:00:00+00:00" }}'::jsonb WHERE id = $1`, testdb.JoinedField.UUID),
		testdb.Bob.ID,
	)

	testsuite.ReindexElastic(ctx)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshCampaigns|models.RefreshGroups)
	assert.NoError(t, err)

	tcs := []struct {
		query              string
		expectedContactIDs []models.ContactID
		expectedEventIDs   []models.ContactID
	}{
		{ // 0
			query:              "cathy",
			expectedContactIDs: []models.ContactID{testdb.Cathy.ID},
			expectedEventIDs:   []models.ContactID{},
		},
		{ // 1
			query:              "bob",
			expectedContactIDs: []models.ContactID{testdb.Bob.ID},
			expectedEventIDs:   []models.ContactID{testdb.Bob.ID},
		},
		{ // 2
			query:              "name = BOB",
			expectedContactIDs: []models.ContactID{testdb.Bob.ID},
			expectedEventIDs:   []models.ContactID{testdb.Bob.ID},
		},
	}

	for i, tc := range tcs {
		err := models.UpdateGroupStatus(ctx, rt.DB, testdb.DoctorsGroup.ID, models.GroupStatusInitializing)
		assert.NoError(t, err)

		count, err := search.PopulateSmartGroup(ctx, rt, oa, testdb.DoctorsGroup.ID, tc.query)
		assert.NoError(t, err, "%d: error populating smart group")
		assert.Equal(t, count, len(tc.expectedContactIDs), "%d: contact count mismatch", i)

		// assert the current group membership
		contactIDs, err := models.GetGroupContactIDs(ctx, rt.DB, testdb.DoctorsGroup.ID)
		assert.NoError(t, err)
		assert.Equal(t, tc.expectedContactIDs, contactIDs)

		assertdb.Query(t, rt.DB, `SELECT count(*) from contacts_contactgroup WHERE id = $1 AND status = 'R'`, testdb.DoctorsGroup.ID).
			Returns(1, "wrong number of contacts in group for query: %s", tc.query)

		assertdb.Query(t, rt.DB, `SELECT count(*) from contacts_contactfire WHERE fire_type = 'C' AND scope = $1::text || ':1'`, newEvent.ID).
			Returns(len(tc.expectedEventIDs), "wrong number of contacts with events for query: %s", tc.query)

		assertdb.Query(t, rt.DB, `SELECT count(*) from contacts_contactfire WHERE fire_type = 'C' AND scope = $1::text || ':1' AND contact_id = ANY($2)`, newEvent.ID, pq.Array(tc.expectedEventIDs)).
			Returns(len(tc.expectedEventIDs), "wrong contacts with events for query: %s", tc.query)
	}
}
