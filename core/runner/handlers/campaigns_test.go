package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestCampaigns(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	doctors := assets.NewGroupReference(testdb.DoctorsGroup.UUID, "Doctors")
	joined := assets.NewFieldReference("joined", "Joined")

	// insert an event on our campaign that is based on created_on
	testdb.InsertCampaignFlowPoint(rt, testdb.RemindersCampaign, testdb.Favorites, testdb.CreatedOnField, 1000, "W")

	// insert an event on our campaign that is based on last_seen_on
	testdb.InsertCampaignFlowPoint(rt, testdb.RemindersCampaign, testdb.Favorites, testdb.LastSeenOnField, 2, "D")

	// joined + 1 week => Pick A Number
	// joined + 5 days => Favorites
	// joined + 10 minutes => "Hi @contact.name, it is time to consult with your patients."
	// created_on + 1000 weeks => Favorites
	// last_seen_on + 2 days => Favorites

	msg1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "Hi there", models.MsgStatusPending)

	tcs := []handlers.TestCase{
		{
			Msgs: handlers.ContactMsgMap{
				testdb.Cathy: msg1,
			},
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewRemoveContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}, false),
					actions.NewAddContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}),
					actions.NewSetContactField(handlers.NewActionUUID(), joined, "2029-09-15T12:00:00+00:00"),
					actions.NewSetContactField(handlers.NewActionUUID(), joined, ""),
				},
				testdb.Bob: []flows.Action{
					actions.NewAddContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}),
					actions.NewSetContactField(handlers.NewActionUUID(), joined, "2029-09-15T12:00:00+00:00"),
					actions.NewSetContactField(handlers.NewActionUUID(), joined, "2029-09-15T12:00:00+00:00"),
				},
				testdb.George: []flows.Action{
					actions.NewAddContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}),
					actions.NewSetContactField(handlers.NewActionUUID(), joined, "2029-09-15T12:00:00+00:00"),
					actions.NewRemoveContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}, false),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{ // 2 new events on created_on and last_seen_on
					SQL:   `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`,
					Args:  []any{testdb.Cathy.ID},
					Count: 2,
				},
				{ // 3 events on joined_on + new event on created_on
					SQL:   `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`,
					Args:  []any{testdb.Bob.ID},
					Count: 4,
				},
				{ // no events because removed from doctors
					SQL:   `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`,
					Args:  []any{testdb.George.ID},
					Count: 0,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
