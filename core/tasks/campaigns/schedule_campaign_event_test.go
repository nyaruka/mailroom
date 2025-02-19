package campaigns_test

import (
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/campaigns"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleCampaignEvent(t *testing.T) {
	_, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	// add bob, george and alexandria to doctors group which campaign is based on
	testdata.DoctorsGroup.Add(rt, testdata.Bob, testdata.George, testdata.Alexandria)

	// give bob and george values for joined in the future
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"d83aae24-4bbf-49d0-ab85-6bfd201eac6d": {"datetime": "2030-01-01T00:00:00Z"}}' WHERE id = $1`, testdata.Bob.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"d83aae24-4bbf-49d0-ab85-6bfd201eac6d": {"datetime": "2030-08-18T11:31:30Z"}}' WHERE id = $1`, testdata.George.ID)

	// give alexandria a value in the past
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"d83aae24-4bbf-49d0-ab85-6bfd201eac6d": {"datetime": "2015-01-01T00:00:00Z"}}' WHERE id = $1`, testdata.Alexandria.ID)

	// campaign has two events configured on the joined field
	//  1. +5 Days (12:00) start favorites flow
	//  2. +10 Minutes send message

	// schedule first event...
	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: testdata.RemindersEvent1.ID})
	testsuite.FlushTasks(t, rt)

	// cathy has no value for joined and alexandia has a value too far in past, but bob and george will have values...
	assertContactFires(t, rt.DB, testdata.RemindersEvent1.ID, map[models.ContactID]time.Time{
		testdata.Bob.ID:    time.Date(2030, 1, 5, 20, 0, 0, 0, time.UTC),  // 12:00 in PST
		testdata.George.ID: time.Date(2030, 8, 23, 19, 0, 0, 0, time.UTC), // 12:00 in PST with DST
	})

	// schedule second event...
	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: testdata.RemindersEvent2.ID})
	testsuite.FlushTasks(t, rt)

	assertContactFires(t, rt.DB, testdata.RemindersEvent2.ID, map[models.ContactID]time.Time{
		testdata.Bob.ID:    time.Date(2030, 1, 1, 0, 10, 0, 0, time.UTC),
		testdata.George.ID: time.Date(2030, 8, 18, 11, 42, 0, 0, time.UTC),
	})

	// fires for first event unaffected
	assertContactFires(t, rt.DB, testdata.RemindersEvent1.ID, map[models.ContactID]time.Time{
		testdata.Bob.ID:    time.Date(2030, 1, 5, 20, 0, 0, 0, time.UTC),
		testdata.George.ID: time.Date(2030, 8, 23, 19, 0, 0, 0, time.UTC),
	})

	// remove alexandria from campaign group
	rt.DB.MustExec(`DELETE FROM contacts_contactgroup_contacts WHERE contact_id = $1`, testdata.Alexandria.ID)

	// bump created_on for cathy and alexandria
	rt.DB.MustExec(`UPDATE contacts_contact SET created_on = '2035-01-01T00:00:00Z' WHERE id = $1 OR id = $2`, testdata.Cathy.ID, testdata.Alexandria.ID)

	// create new campaign event based on created_on + 5 minutes
	event3 := testdata.InsertCampaignFlowEvent(rt, testdata.RemindersCampaign, testdata.Favorites, testdata.CreatedOnField, 5, "M")

	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: event3.ID})
	testsuite.FlushTasks(t, rt)

	// only cathy is in the group and new enough to have a fire
	assertContactFires(t, rt.DB, event3.ID, map[models.ContactID]time.Time{
		testdata.Cathy.ID: time.Date(2035, 1, 1, 0, 5, 0, 0, time.UTC),
	})

	// create new campaign event based on last_seen_on + 1 day
	event4 := testdata.InsertCampaignFlowEvent(rt, testdata.RemindersCampaign, testdata.Favorites, testdata.LastSeenOnField, 1, "D")

	// bump last_seen_on for bob
	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = '2040-01-01T00:00:00Z' WHERE id = $1`, testdata.Bob.ID)

	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: event4.ID})
	testsuite.FlushTasks(t, rt)

	assertContactFires(t, rt.DB, event4.ID, map[models.ContactID]time.Time{
		testdata.Bob.ID: time.Date(2040, 1, 2, 0, 0, 0, 0, time.UTC),
	})
}

func assertContactFires(t *testing.T, db *sqlx.DB, eventID models.CampaignEventID, expected map[models.ContactID]time.Time) {
	type idAndTime struct {
		ContactID models.ContactID `db:"contact_id"`
		FireOn    time.Time        `db:"fire_on"`
	}

	actualAsSlice := make([]idAndTime, 0)
	err := db.Select(&actualAsSlice, `SELECT contact_id, fire_on FROM contacts_contactfire WHERE scope = $1::text`, eventID)
	require.NoError(t, err)

	actual := make(map[models.ContactID]time.Time)
	for _, it := range actualAsSlice {
		actual[it.ContactID] = it.FireOn
	}

	assert.Equal(t, expected, actual)
}
