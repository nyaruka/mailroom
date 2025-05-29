package ctasks_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestMsgReceivedTask(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetAll)

	ivr.RegisterService(models.ChannelType("T"), testsuite.NewIVRServiceFactory)

	// create a disabled channel
	disabled := testdata.InsertChannel(rt, testdata.Org1, "TG", "Deleted", "1234567", []string{"telegram"}, "SR", map[string]any{})
	rt.DB.MustExec(`UPDATE channels_channel SET is_enabled = false WHERE id = $1`, disabled.ID)

	testdata.InsertKeywordTrigger(rt, testdata.Org1, testdata.Favorites, []string{"start"}, models.MatchOnly, nil, nil, nil)
	testdata.InsertKeywordTrigger(rt, testdata.Org1, testdata.IVRFlow, []string{"ivr"}, models.MatchOnly, nil, nil, nil)

	testdata.InsertKeywordTrigger(rt, testdata.Org2, testdata.Org2Favorites, []string{"start"}, models.MatchOnly, nil, nil, nil)
	testdata.InsertCatchallTrigger(rt, testdata.Org2, testdata.Org2SingleMessage, nil, nil, nil)

	// create a blocked contact
	blocked := testdata.InsertContact(rt, testdata.Org1, "2fc8601a-93eb-43a1-892c-9ff5fa291357", "Blocked", "eng", models.ContactStatusBlocked)

	// create a deleted contact
	deleted := testdata.InsertContact(rt, testdata.Org1, "116e40b1-cecb-4be7-9dea-1a21141a05bc", "Del", "eng", models.ContactStatusActive)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = false WHERE id = $1`, deleted.ID)

	// give Cathy, Bob and the blocked contact some tickets...
	openTickets := map[*testdata.Contact][]*testdata.Ticket{
		testdata.Cathy: {
			testdata.InsertOpenTicket(rt, testdata.Org1, testdata.Cathy, testdata.DefaultTopic, time.Now(), nil),
		},
		blocked: {
			testdata.InsertOpenTicket(rt, testdata.Org1, blocked, testdata.DefaultTopic, time.Now(), nil),
		},
	}
	closedTickets := map[*testdata.Contact][]*testdata.Ticket{
		testdata.Cathy: {
			testdata.InsertClosedTicket(rt, testdata.Org1, testdata.Cathy, testdata.DefaultTopic, nil),
		},
		testdata.Bob: {
			testdata.InsertClosedTicket(rt, testdata.Org1, testdata.Bob, testdata.DefaultTopic, nil),
		},
	}

	rt.DB.MustExec(`UPDATE tickets_ticket SET last_activity_on = '2021-01-01T00:00:00Z'`)

	// clear all of Alexandria's URNs
	rt.DB.MustExec(`UPDATE contacts_contacturn SET contact_id = NULL WHERE contact_id = $1`, testdata.Alexandra.ID)

	models.FlushCache()

	// insert a dummy message into the database that will get the updates from handling each message event which pretends to be it
	dbMsg := testdata.InsertIncomingMsg(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy, "", models.MsgStatusPending)

	tcs := []struct {
		preHook             func()
		org                 *testdata.Org
		channel             *testdata.Channel
		contact             *testdata.Contact
		text                string
		expectedVisibility  models.MsgVisibility
		expectedReplyText   string
		expectedReplyStatus models.MsgStatus
		expectedFlow        *testdata.Flow
	}{
		{ // 0: no trigger match, inbox message
			org:                testdata.Org1,
			channel:            testdata.FacebookChannel,
			contact:            testdata.Cathy,
			text:               "noop",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 1: no trigger match, inbox message (trigger is keyword only)
			org:                testdata.Org1,
			channel:            testdata.FacebookChannel,
			contact:            testdata.Cathy,
			text:               "start other",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 2: keyword trigger match, flow message
			org:                 testdata.Org1,
			channel:             testdata.FacebookChannel,
			contact:             testdata.Cathy,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Favorites,
		},
		{ // 3:
			org:                 testdata.Org1,
			channel:             testdata.FacebookChannel,
			contact:             testdata.Cathy,
			text:                "purple",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "I don't know that color. Try again.",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Favorites,
		},
		{ // 4:
			org:                 testdata.Org1,
			channel:             testdata.FacebookChannel,
			contact:             testdata.Cathy,
			text:                "blue",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Good choice, I like Blue too! What is your favorite beer?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Favorites,
		},
		{ // 5:
			org:                 testdata.Org1,
			channel:             testdata.FacebookChannel,
			contact:             testdata.Cathy,
			text:                "MUTZIG",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Mmmmm... delicious Mutzig. If only they made blue Mutzig! Lastly, what is your name?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Favorites,
		},
		{ // 6:
			org:                 testdata.Org1,
			channel:             testdata.FacebookChannel,
			contact:             testdata.Cathy,
			text:                "Cathy",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Thanks Cathy, we are all done!",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Favorites,
		},
		{ // 7:
			org:                testdata.Org1,
			channel:            testdata.FacebookChannel,
			contact:            testdata.Cathy,
			text:               "noop",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 8:
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "other",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Hey, how are you?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2SingleMessage,
		},
		{ // 9:
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2Favorites,
		},
		{ // 10:
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "green",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Good choice, I like Green too! What is your favorite beer?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2Favorites,
		},
		{ // 11:
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "primus",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Mmmmm... delicious Primus. If only they made green Primus! Lastly, what is your name?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2Favorites,
		},
		{ // 12:
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "george",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Thanks george, we are all done!",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2Favorites,
		},
		{ // 13:
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "blargh",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Hey, how are you?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2SingleMessage,
		},
		{ // 14:
			org:                testdata.Org1,
			channel:            testdata.FacebookChannel,
			contact:            testdata.Bob,
			text:               "ivr",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 15: stopped contact should be unstopped
			preHook: func() {
				rt.DB.MustExec(`UPDATE contacts_contact SET status = 'S' WHERE id = $1`, testdata.George.ID)
			},
			org:                 testdata.Org1,
			channel:             testdata.FacebookChannel,
			contact:             testdata.George,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Favorites,
		},
		{ // 16: no URN on contact but failed reply created anyway
			org:                 testdata.Org1,
			channel:             testdata.TwilioChannel,
			contact:             testdata.Alexandra,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusFailed,
			expectedFlow:        testdata.Favorites,
		},
		{ // 17: start Fred back in our favorite flow, then make it inactive, will be handled by catch-all
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2Favorites,
		},
		{ // 18:
			preHook: func() {
				rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE WHERE id = $1`, testdata.Org2Favorites.ID)
			},
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "red",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Hey, how are you?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2SingleMessage,
		},
		{ // 19: start Fred back in our favorites flow to test retries
			preHook: func() {
				rt.DB.MustExec(`UPDATE flows_flow SET is_active = TRUE WHERE id = $1`, testdata.Org2Favorites.ID)
			},
			org:                 testdata.Org2,
			channel:             testdata.Org2Channel,
			contact:             testdata.Org2Contact,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdata.Org2Favorites,
		},
		{ // 20: deleted contact
			org:     testdata.Org1,
			channel: testdata.TwilioChannel,
			contact: deleted,
			text:    "start",
		},
		{ // 21: blocked contact
			org:                testdata.Org1,
			channel:            testdata.FacebookChannel,
			contact:            blocked,
			text:               "start",
			expectedVisibility: models.VisibilityArchived,
		},
		{ // 22: disabled channel
			org:                testdata.Org1,
			channel:            disabled,
			contact:            testdata.Cathy,
			text:               "start",
			expectedVisibility: models.VisibilityArchived,
		},
	}

	makeMsgTask := func(channel *testdata.Channel, contact *testdata.Contact, text string) handler.Task {
		return &ctasks.MsgReceivedTask{
			ChannelID: channel.ID,
			MsgID:     dbMsg.ID,
			MsgUUID:   dbMsg.FlowMsg.UUID(),
			URN:       contact.URN,
			URNID:     contact.URNID,
			Text:      text,
		}
	}

	last := time.Now()

	for i, tc := range tcs {
		models.FlushCache()

		// reset our dummy db message into an unhandled state
		rt.DB.MustExec(`UPDATE msgs_msg SET status = 'P', flow_id = NULL WHERE id = $1`, dbMsg.ID)

		// run our setup hook if we have one
		if tc.preHook != nil {
			tc.preHook()
		}

		err := handler.QueueTask(rc, tc.org.ID, tc.contact.ID, makeMsgTask(tc.channel, tc.contact, tc.text))
		assert.NoError(t, err, "%d: error adding task", i)

		task, err := tasks.HandlerQueue.Pop(rc)
		assert.NoError(t, err, "%d: error popping next task", i)

		err = tasks.Perform(ctx, rt, task)
		assert.NoError(t, err, "%d: error when handling event", i)

		var expectedFlowID any
		if tc.expectedFlow != nil {
			expectedFlowID = int64(tc.expectedFlow.ID)
		}

		// check that message is marked as handled
		if tc.contact != deleted {
			assertdb.Query(t, rt.DB, `SELECT status, visibility, msg_type, flow_id FROM msgs_msg WHERE id = $1`, dbMsg.ID).
				Columns(map[string]any{"status": "H", "visibility": string(tc.expectedVisibility), "msg_type": "T", "flow_id": expectedFlowID}, "%d: msg state mismatch", i)
		}

		// if we are meant to have a reply, check it
		if tc.expectedReplyText != "" {
			assertdb.Query(t, rt.DB, `SELECT text, status FROM msgs_msg WHERE contact_id = $1 AND created_on > $2 ORDER BY id DESC LIMIT 1`, tc.contact.ID, last).
				Columns(map[string]any{"text": tc.expectedReplyText, "status": string(tc.expectedReplyStatus)}, "%d: reply mismatch", i)
		} else {
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND created_on > $2`, tc.contact.ID, last).Returns(0, "%d: unexpected reply", i)
		}

		// check last open ticket for this contact was updated unless contact is blocked
		numOpenTickets := len(openTickets[tc.contact])
		if tc.contact != blocked {
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE contact_id = $1 AND status = 'O' AND last_activity_on > $2`, tc.contact.ID, last).
				Returns(numOpenTickets, "%d: updated open ticket mismatch", i)
		} else {
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE contact_id = $1 AND status = 'O' AND last_activity_on = '2021-01-01T00:00:00Z'`, tc.contact.ID).
				Returns(numOpenTickets, "%d: not updated open ticket mismatch", i)
		}

		// check any closed tickets are unchanged
		numClosedTickets := len(closedTickets[tc.contact])
		assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE contact_id = $1 AND status = 'C' AND last_activity_on = '2021-01-01T00:00:00Z'`, tc.contact.ID).
			Returns(numClosedTickets, "%d: unchanged closed ticket mismatch", i)

		last = time.Now()
	}

	// check messages queued to courier
	testsuite.AssertCourierQueues(t, map[string][]int{
		fmt.Sprintf("msgs:%s|10/1", testdata.FacebookChannel.UUID): {1, 1, 1, 1, 1, 1},
		fmt.Sprintf("msgs:%s|10/1", testdata.Org2Channel.UUID):     {1, 1, 1, 1, 1, 1, 1, 1, 1},
	})

	// Fred's sessions should not have a timeout because courier will set them
	assertdb.Query(t, rt.DB, `SELECT count(*) from contacts_contactfire where contact_id = $1 and fire_type = 'T'`, testdata.Org2Contact.ID).Returns(0)

	// force an error by marking our run for fred as complete (our session is still active so this will blow up)
	rt.DB.MustExec(`UPDATE flows_flowrun SET status = 'C', exited_on = NOW() WHERE contact_id = $1`, testdata.Org2Contact.ID)
	handler.QueueTask(rc, testdata.Org2.ID, testdata.Org2Contact.ID, makeMsgTask(testdata.Org2Channel, testdata.Org2Contact, "red"))

	// should get requeued three times automatically
	for i := 0; i < 3; i++ {
		task, _ := tasks.HandlerQueue.Pop(rc)
		assert.NotNil(t, task)
		err := tasks.Perform(ctx, rt, task)
		assert.NoError(t, err)
	}

	// on third error, no new task
	task, err := tasks.HandlerQueue.Pop(rc)
	assert.NoError(t, err)
	assert.Nil(t, task)

	// mark Fred's flow as inactive
	rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE where id = $1`, testdata.Org2Favorites.ID)
	models.FlushCache()

	// try to resume now
	handler.QueueTask(rc, testdata.Org2.ID, testdata.Org2Contact.ID, makeMsgTask(testdata.Org2Channel, testdata.Org2Contact, "red"))
	task, _ = tasks.HandlerQueue.Pop(rc)
	assert.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	assert.NoError(t, err)

	// should get our catch all trigger
	assertdb.Query(t, rt.DB, `SELECT text FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' ORDER BY id DESC LIMIT 1`, testdata.Org2Contact.ID).Returns("Hey, how are you?")
	previous := time.Now()

	// and should have failed previous session
	assertdb.Query(t, rt.DB, `SELECT count(*) from flows_flowsession where contact_id = $1 and status = 'F'`, testdata.Org2Contact.ID).Returns(2)

	// trigger should also not start a new session
	handler.QueueTask(rc, testdata.Org2.ID, testdata.Org2Contact.ID, makeMsgTask(testdata.Org2Channel, testdata.Org2Contact, "start"))
	task, _ = tasks.HandlerQueue.Pop(rc)
	err = tasks.Perform(ctx, rt, task)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND created_on > $2`, testdata.Org2Contact.ID, previous).Returns(0)
}
