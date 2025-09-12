package search_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRecipientsQuery(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	dates.SetNowFunc(dates.NewFixedNow(time.Date(2022, 4, 20, 15, 30, 45, 0, time.UTC)))
	defer dates.SetNowFunc(time.Now)

	oa := testdb.Org1.Load(rt)
	flow, err := oa.FlowByID(testdb.Favorites.ID)
	require.NoError(t, err)

	doctors := oa.GroupByID(testdb.DoctorsGroup.ID)
	testers := oa.GroupByID(testdb.TestersGroup.ID)

	tcs := []struct {
		groups        []*models.Group
		contactUUIDs  []flows.ContactUUID
		userQuery     string
		exclusions    models.Exclusions
		excludeGroups []*models.Group
		expected      string
		err           string
	}{
		{ // 0
			groups:       []*models.Group{doctors, testers},
			contactUUIDs: []flows.ContactUUID{testdb.Ann.UUID, testdb.George.UUID},
			exclusions:   models.Exclusions{},
			expected:     `group = "Doctors" OR group = "Testers" OR uuid = "6393abc0-283d-4c9b-a1b3-641a035c34bf" OR uuid = "8d024bcd-f473-4719-a00a-bd0bb1190135"`,
		},
		{ // 1
			groups:       []*models.Group{doctors},
			contactUUIDs: []flows.ContactUUID{testdb.Ann.UUID},
			exclusions: models.Exclusions{
				NonActive:         true,
				InAFlow:           true,
				StartedPreviously: true,
				NotSeenSinceDays:  90,
			},
			excludeGroups: []*models.Group{testers},
			expected:      `(group = "Doctors" OR uuid = "6393abc0-283d-4c9b-a1b3-641a035c34bf") AND status = "active" AND flow = "" AND history != "Favorites" AND last_seen_on > "20-01-2022" AND group != "Testers"`,
		},
		{ // 2
			contactUUIDs: []flows.ContactUUID{testdb.Ann.UUID},
			exclusions: models.Exclusions{
				NonActive: true,
			},
			expected: `uuid = "6393abc0-283d-4c9b-a1b3-641a035c34bf" AND status = "active"`,
		},
		{ // 3
			userQuery:  `fields.GENDER = "M"`,
			exclusions: models.Exclusions{},
			expected:   `fields.gender = "M"`,
		},
		{ // 4
			userQuery: `gender = "M"`,
			exclusions: models.Exclusions{
				NonActive:         true,
				InAFlow:           true,
				StartedPreviously: true,
				NotSeenSinceDays:  30,
			},
			expected: `fields.gender = "M" AND status = "active" AND flow = "" AND history != "Favorites" AND last_seen_on > "21-03-2022"`,
		},
		{ // 5
			userQuery: `name ~ ben`,
			exclusions: models.Exclusions{
				NonActive:         false,
				InAFlow:           false,
				StartedPreviously: false,
				NotSeenSinceDays:  30,
			},
			expected: `name ~ "ben" AND last_seen_on > "21-03-2022"`,
		},
		{ // 6
			userQuery: `name ~ ben OR name ~ eric`,
			exclusions: models.Exclusions{
				NonActive:         false,
				InAFlow:           false,
				StartedPreviously: false,
				NotSeenSinceDays:  30,
			},
			expected: `(name ~ "ben" OR name ~ "eric") AND last_seen_on > "21-03-2022"`,
		},
		{ // 7
			userQuery:  `name ~`, // syntactically invalid user query
			exclusions: models.Exclusions{},
			err:        "invalid user query: mismatched input '<EOF>' expecting {STRING, PROPERTY, TEXT}",
		},
		{ // 8
			userQuery:  `goats > 14`, // no such field
			exclusions: models.Exclusions{},
			err:        "invalid user query: can't resolve 'goats' to attribute, scheme or field",
		},
		{ // 9
			userQuery:  `fields.goats > 14`, // type prefix but no such field
			exclusions: models.Exclusions{},
			err:        "invalid user query: can't resolve 'goats' to attribute, scheme or field",
		},
	}

	for i, tc := range tcs {
		actual, err := search.BuildRecipientsQuery(oa, flow, tc.groups, tc.contactUUIDs, tc.userQuery, tc.exclusions, tc.excludeGroups)
		if tc.err != "" {
			assert.Equal(t, "", actual)
			assert.EqualError(t, err, tc.err, "%d: error mismatch", i)
		} else {
			assert.Equal(t, tc.expected, actual, "%d: query mismatch", i)
			assert.NoError(t, err)
		}
	}
}
