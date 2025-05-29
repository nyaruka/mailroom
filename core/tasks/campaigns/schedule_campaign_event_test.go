package campaigns_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/tasks/campaigns"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
)

func TestScheduleCampaignEvent(t *testing.T) {
	_, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	// set campaign event status to (S)CHEDULING (done by RP)
	rt.DB.MustExec(`UPDATE campaigns_campaignevent SET status = 'S' WHERE id = $1`, testdata.RemindersEvent1.ID)

	// add bob, george and alexandria to doctors group which campaign is based on
	testdata.DoctorsGroup.Add(rt, testdata.Bob, testdata.George, testdata.Alexandra)

	// give bob and george values for joined in the future
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"d83aae24-4bbf-49d0-ab85-6bfd201eac6d": {"datetime": "2030-01-01T00:00:00Z"}}' WHERE id = $1`, testdata.Bob.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"d83aae24-4bbf-49d0-ab85-6bfd201eac6d": {"datetime": "2030-08-18T11:31:30Z"}}' WHERE id = $1`, testdata.George.ID)

	// give alexandria a value in the past
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"d83aae24-4bbf-49d0-ab85-6bfd201eac6d": {"datetime": "2015-01-01T00:00:00Z"}}' WHERE id = $1`, testdata.Alexandra.ID)

	// campaign has two events configured on the joined field
	//  1. +5 Days (12:00) start favorites flow
	//  2. +10 Minutes send message

	// schedule first event...
	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: testdata.RemindersEvent1.ID})
	testsuite.FlushTasks(t, rt)

	// cathy has no value for joined and alexandia has a value too far in past, but bob and george will have values...
	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{
		"C/10000:1": time.Date(2030, 1, 5, 20, 0, 0, 0, time.UTC), // 12:00 in PST
	})
	testsuite.AssertContactFires(t, rt, testdata.George.ID, map[string]time.Time{
		"C/10000:1": time.Date(2030, 8, 23, 19, 0, 0, 0, time.UTC), // 12:00 in PST with DST
	})

	// campaign event itself is now marked as (R)EADY
	assertdb.Query(t, rt.DB, `SELECT status FROM campaigns_campaignevent WHERE id = $1`, testdata.RemindersEvent1.ID).Returns("R")

	// schedule second event...
	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: testdata.RemindersEvent2.ID})
	testsuite.FlushTasks(t, rt)

	// fires for first event unaffected
	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{
		"C/10000:1": time.Date(2030, 1, 5, 20, 0, 0, 0, time.UTC),
		"C/10001:1": time.Date(2030, 1, 1, 0, 10, 0, 0, time.UTC),
	})
	testsuite.AssertContactFires(t, rt, testdata.George.ID, map[string]time.Time{
		"C/10000:1": time.Date(2030, 8, 23, 19, 0, 0, 0, time.UTC),
		"C/10001:1": time.Date(2030, 8, 18, 11, 42, 0, 0, time.UTC),
	})

	// remove alexandria from campaign group
	rt.DB.MustExec(`DELETE FROM contacts_contactgroup_contacts WHERE contact_id = $1`, testdata.Alexandra.ID)

	// bump created_on for cathy and alexandria
	rt.DB.MustExec(`UPDATE contacts_contact SET created_on = '2035-01-01T00:00:00Z' WHERE id = $1 OR id = $2`, testdata.Cathy.ID, testdata.Alexandra.ID)

	// create new campaign event based on created_on + 5 minutes
	event3 := testdata.InsertCampaignFlowEvent(rt, testdata.RemindersCampaign, testdata.Favorites, testdata.CreatedOnField, 5, "M")

	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: event3.ID})
	testsuite.FlushTasks(t, rt)

	// only cathy is in the group and new enough to have a fire
	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{
		"C/10000:1": time.Date(2030, 1, 5, 20, 0, 0, 0, time.UTC),
		"C/10001:1": time.Date(2030, 1, 1, 0, 10, 0, 0, time.UTC),
	})
	testsuite.AssertContactFires(t, rt, testdata.George.ID, map[string]time.Time{
		"C/10000:1": time.Date(2030, 8, 23, 19, 0, 0, 0, time.UTC),
		"C/10001:1": time.Date(2030, 8, 18, 11, 42, 0, 0, time.UTC),
	})
	testsuite.AssertContactFires(t, rt, testdata.Cathy.ID, map[string]time.Time{
		"C/30000:1": time.Date(2035, 1, 1, 0, 5, 0, 0, time.UTC),
	})

	// create new campaign event based on last_seen_on + 1 day
	event4 := testdata.InsertCampaignFlowEvent(rt, testdata.RemindersCampaign, testdata.Favorites, testdata.LastSeenOnField, 1, "D")

	// bump last_seen_on for bob
	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = '2040-01-01T00:00:00Z' WHERE id = $1`, testdata.Bob.ID)

	testsuite.QueueBatchTask(t, rt, testdata.Org1, &campaigns.ScheduleCampaignEventTask{CampaignEventID: event4.ID})
	testsuite.FlushTasks(t, rt)

	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{
		"C/10000:1": time.Date(2030, 1, 5, 20, 0, 0, 0, time.UTC),
		"C/10001:1": time.Date(2030, 1, 1, 0, 10, 0, 0, time.UTC),
		"C/30001:1": time.Date(2040, 1, 2, 0, 0, 0, 0, time.UTC),
	})
}
