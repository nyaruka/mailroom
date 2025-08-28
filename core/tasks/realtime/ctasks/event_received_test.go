package ctasks_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelEvents(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// schedule a campaign fires for cathy and george
	testdb.InsertContactFire(rt, testdb.Org1, testdb.Cathy, models.ContactFireTypeCampaignPoint, fmt.Sprint(testdb.RemindersPoint1), time.Now(), "")
	testdb.InsertContactFire(rt, testdb.Org1, testdb.George, models.ContactFireTypeCampaignPoint, fmt.Sprint(testdb.RemindersPoint1), time.Now(), "")

	// and george to doctors group, cathy is already part of it
	rt.DB.MustExec(`INSERT INTO contacts_contactgroup_contacts(contactgroup_id, contact_id) VALUES($1, $2);`, testdb.DoctorsGroup.ID, testdb.George.ID)

	// add some channel event triggers
	testdb.InsertNewConversationTrigger(rt, testdb.Org1, testdb.Favorites, testdb.FacebookChannel)
	testdb.InsertReferralTrigger(rt, testdb.Org1, testdb.PickANumber, "", testdb.VonageChannel)
	testdb.InsertOptInTrigger(rt, testdb.Org1, testdb.Favorites, testdb.VonageChannel)
	testdb.InsertOptOutTrigger(rt, testdb.Org1, testdb.PickANumber, testdb.VonageChannel)

	polls := testdb.InsertOptIn(rt, testdb.Org1, "Polls")

	// add a URN for cathy so we can test twitter URNs
	testdb.InsertContactURN(rt, testdb.Org1, testdb.Bob, urns.URN("twitterid:123456"), 10, nil)

	// create a deleted contact
	deleted := testdb.InsertContact(rt, testdb.Org1, "", "Del", "eng", models.ContactStatusActive)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = false WHERE id = $1`, deleted.ID)

	// insert a dummy event into the database that will get the updates from handling each event which pretends to be it
	eventID := testdb.InsertChannelEvent(rt, testdb.Org1, models.EventTypeMissedCall, testdb.TwilioChannel, testdb.Cathy, models.EventStatusPending)

	tcs := []struct {
		contact             *testdb.Contact
		task                realtime.Task
		expectedTriggerType string
		expectedResponse    string
		persistedEvents     map[flows.ContactUUID][]string
	}{
		{ // 0: new conversation on Facebook
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeNewConversation,
				ChannelID:  testdb.FacebookChannel.ID,
				URNID:      testdb.Cathy.URNID,
				Extra:      null.Map[any]{},
				NewContact: false,
			},
			expectedTriggerType: "chat",
			expectedResponse:    "What is your favorite color?",
			persistedEvents:     map[flows.ContactUUID][]string{testdb.Cathy.UUID: {"chat_started", "run_started"}},
		},
		{ // 1: new conversation on Vonage (no trigger)
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeNewConversation,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				Extra:      null.Map[any]{},
				NewContact: false,
			},
			expectedTriggerType: "",
			expectedResponse:    "",
			persistedEvents:     map[flows.ContactUUID][]string{testdb.Cathy.UUID: {"chat_started"}},
		},
		{ // 2: welcome message on Vonage
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeWelcomeMessage,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				Extra:      null.Map[any]{},
				NewContact: false,
			},
			expectedTriggerType: "",
			expectedResponse:    "",
			persistedEvents:     map[flows.ContactUUID][]string{},
		},
		{ // 3: referral on Facebook
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeReferral,
				ChannelID:  testdb.FacebookChannel.ID,
				URNID:      testdb.Cathy.URNID,
				Extra:      null.Map[any]{"referrer_id": "123456"},
				NewContact: false,
			},
			expectedTriggerType: "",
			expectedResponse:    "",
			persistedEvents:     map[flows.ContactUUID][]string{testdb.Cathy.UUID: {"chat_started"}},
		},
		{ // 4: referral on Facebook
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeReferral,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				Extra:      null.Map[any]{"referrer_id": "123456"},
				NewContact: false,
			},
			expectedTriggerType: "chat",
			expectedResponse:    "Pick a number between 1-10.",
			persistedEvents:     map[flows.ContactUUID][]string{testdb.Cathy.UUID: {"chat_started", "run_ended", "run_started"}},
		},
		{ // 5: optin on Vonage
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeOptIn,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				OptInID:    polls.ID,
				Extra:      map[string]any{"title": "Polls", "payload": fmt.Sprint(polls.ID)},
				NewContact: false,
			},
			expectedTriggerType: "optin",
			expectedResponse:    "What is your favorite color?",
			persistedEvents:     map[flows.ContactUUID][]string{testdb.Cathy.UUID: {"optin_started", "run_ended", "run_started"}},
		},
		{ // 6: optout on Vonage
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeOptOut,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				OptInID:    polls.ID,
				Extra:      map[string]any{"title": "Polls", "payload": fmt.Sprint(polls.ID)},
				NewContact: false,
			},
			expectedTriggerType: "optin",
			expectedResponse:    "Pick a number between 1-10.",
			persistedEvents:     map[flows.ContactUUID][]string{testdb.Cathy.UUID: {"optin_stopped", "run_ended", "run_started"}},
		},
		{ // 7: missed call trigger queued by RP
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeMissedCall,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				OptInID:    polls.ID,
				Extra:      map[string]any{"duration": 123},
				NewContact: false,
			},
			expectedTriggerType: "",
			expectedResponse:    "",
			persistedEvents:     map[flows.ContactUUID][]string{testdb.Cathy.UUID: {"call_missed"}},
		},
		{ // 8: stop contact
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeStopContact,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				Extra:      null.Map[any]{},
				NewContact: false,
			},
			expectedTriggerType: "",
			expectedResponse:    "",
			persistedEvents:     map[flows.ContactUUID][]string{},
		},
		{ // 9: a task against a deleted contact
			contact: deleted,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeNewConversation,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      deleted.URNID,
				Extra:      null.Map[any]{},
				NewContact: false,
			},
			expectedTriggerType: "",
			expectedResponse:    "",
			persistedEvents:     map[flows.ContactUUID][]string{},
		},
		{ // 10: task to delete contact
			contact: testdb.Cathy,
			task: &ctasks.EventReceivedTask{
				EventID:    eventID,
				EventType:  models.EventTypeDeleteContact,
				ChannelID:  testdb.VonageChannel.ID,
				URNID:      testdb.Cathy.URNID,
				Extra:      null.Map[any]{},
				NewContact: false,
			},
			expectedTriggerType: "",
			expectedResponse:    "",
			persistedEvents:     map[flows.ContactUUID][]string{},
		},
	}

	models.FlushCache()

	lastLastSeenOn := time.Now().In(time.UTC).Add(-time.Hour)
	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = $2 WHERE id = $1`, testdb.Cathy.ID, lastLastSeenOn)

	for i, tc := range tcs {
		tc.task.(*ctasks.EventReceivedTask).CreatedOn = time.Now()

		start := time.Now()
		time.Sleep(time.Millisecond * 5)

		// reset our dummy db event into an unhandled state
		rt.DB.MustExec(`UPDATE channels_channelevent SET status = 'P' WHERE id = $1`, eventID)

		err := realtime.QueueTask(ctx, rt, testdb.Org1.ID, tc.contact.ID, tc.task)
		assert.NoError(t, err, "%d: error adding task", i)

		task, err := rt.Queues.Realtime.Pop(ctx, vc)
		assert.NoError(t, err, "%d: error popping next task", i)

		err = tasks.Perform(ctx, rt, task)
		assert.NoError(t, err, "%d: error when handling event", i)

		// check that event is marked as handled
		if tc.contact != deleted {
			assertdb.Query(t, rt.DB, `SELECT status FROM channels_channelevent WHERE id = $1`, eventID).Columns(map[string]any{"status": "H"}, "%d: event state mismatch", i)
		}

		// if we are meant to trigger a new session...
		if tc.expectedTriggerType != "" {
			if assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND created_on > $2`, tc.contact.UUID, start).Returns(1, "%d: expected new session", i) {
				// get session output to lookup trigger type
				var output []byte
				err = rt.DB.Get(&output, `SELECT output FROM flows_flowsession WHERE contact_uuid = $1 AND created_on > $2`, tc.contact.UUID, start)
				require.NoError(t, err)

				trigType, err := jsonparser.GetString(output, "trigger", "type")
				require.NoError(t, err)
				assert.Equal(t, tc.expectedTriggerType, trigType)
			}

			assertdb.Query(t, rt.DB, `SELECT text FROM msgs_msg WHERE contact_id = $1 AND created_on > $2 ORDER BY id DESC LIMIT 1`, tc.contact.ID, start).
				Returns(tc.expectedResponse, "%d: response mismatch", i)
		}

		// check last_seen_on was updated
		if tc.contact != deleted {
			var lastSeen time.Time
			err = rt.DB.Get(&lastSeen, `SELECT last_seen_on FROM contacts_contact WHERE id = $1`, tc.contact.ID)
			assert.NoError(t, err)
			assert.Greater(t, lastSeen, lastLastSeenOn, "%d: expected last seen to be updated", i)
			lastLastSeenOn = lastSeen
		}

		// check persisted events
		persistedEvents := testsuite.GetHistoryEvents(t, rt)
		assert.Equal(t, tc.persistedEvents, persistedEvents, "%d: mismatch in persisted events", i)

		dyntest.Truncate(t, rt.Dynamo, "TestHistory")
	}

	// last event was a stop_contact so check that cathy is stopped
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`, testdb.Cathy.ID).Returns(1)

	// and that only george is left in the group
	assertdb.Query(t, rt.DB, `SELECT count(*) from contacts_contactgroup_contacts WHERE contactgroup_id = $1 AND contact_id = $2`, testdb.DoctorsGroup.ID, testdb.Cathy.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) from contacts_contactgroup_contacts WHERE contactgroup_id = $1 AND contact_id = $2`, testdb.DoctorsGroup.ID, testdb.George.ID).Returns(1)

	// and she has no upcoming events
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`, testdb.Cathy.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'C'`, testdb.George.ID).Returns(1)
}
