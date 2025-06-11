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
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestMsgReceivedTask(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetAll)

	ivr.RegisterService(models.ChannelType("T"), testsuite.NewIVRServiceFactory)

	// create a disabled channel
	disabled := testdb.InsertChannel(rt, testdb.Org1, "TG", "Deleted", "1234567", []string{"telegram"}, "SR", map[string]any{})
	rt.DB.MustExec(`UPDATE channels_channel SET is_enabled = false WHERE id = $1`, disabled.ID)

	testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"start"}, models.MatchOnly, nil, nil, nil)
	testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.IVRFlow, []string{"ivr"}, models.MatchOnly, nil, nil, nil)

	testdb.InsertKeywordTrigger(rt, testdb.Org2, testdb.Org2Favorites, []string{"start"}, models.MatchOnly, nil, nil, nil)
	testdb.InsertCatchallTrigger(rt, testdb.Org2, testdb.Org2SingleMessage, nil, nil, nil)

	// create a blocked contact
	blocked := testdb.InsertContact(rt, testdb.Org1, "2fc8601a-93eb-43a1-892c-9ff5fa291357", "Blocked", "eng", models.ContactStatusBlocked)

	// create a deleted contact
	deleted := testdb.InsertContact(rt, testdb.Org1, "116e40b1-cecb-4be7-9dea-1a21141a05bc", "Del", "eng", models.ContactStatusActive)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = false WHERE id = $1`, deleted.ID)

	// give Cathy, Bob and the blocked contact some tickets...
	openTickets := map[*testdb.Contact][]*testdb.Ticket{
		testdb.Cathy: {
			testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil),
		},
		blocked: {
			testdb.InsertOpenTicket(rt, testdb.Org1, blocked, testdb.DefaultTopic, time.Now(), nil),
		},
	}
	closedTickets := map[*testdb.Contact][]*testdb.Ticket{
		testdb.Cathy: {
			testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil),
		},
		testdb.Bob: {
			testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Bob, testdb.DefaultTopic, nil),
		},
	}

	rt.DB.MustExec(`UPDATE tickets_ticket SET last_activity_on = '2021-01-01T00:00:00Z'`)

	// clear all of Alexandria's URNs
	rt.DB.MustExec(`UPDATE contacts_contacturn SET contact_id = NULL WHERE contact_id = $1`, testdb.Alexandra.ID)

	models.FlushCache()

	// insert a dummy message into the database that will get the updates from handling each message event which pretends to be it
	dbMsg := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "", models.MsgStatusPending)

	tcs := []struct {
		preHook             func()
		org                 *testdb.Org
		channel             *testdb.Channel
		contact             *testdb.Contact
		text                string
		expectedVisibility  models.MsgVisibility
		expectedReplyText   string
		expectedReplyStatus models.MsgStatus
		expectedFlow        *testdb.Flow
	}{
		{ // 0: no trigger match, inbox message
			org:                testdb.Org1,
			channel:            testdb.FacebookChannel,
			contact:            testdb.Cathy,
			text:               "noop",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 1: no trigger match, inbox message (trigger is keyword only)
			org:                testdb.Org1,
			channel:            testdb.FacebookChannel,
			contact:            testdb.Cathy,
			text:               "start other",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 2: keyword trigger match, flow message
			org:                 testdb.Org1,
			channel:             testdb.FacebookChannel,
			contact:             testdb.Cathy,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Favorites,
		},
		{ // 3:
			org:                 testdb.Org1,
			channel:             testdb.FacebookChannel,
			contact:             testdb.Cathy,
			text:                "purple",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "I don't know that color. Try again.",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Favorites,
		},
		{ // 4:
			org:                 testdb.Org1,
			channel:             testdb.FacebookChannel,
			contact:             testdb.Cathy,
			text:                "blue",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Good choice, I like Blue too! What is your favorite beer?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Favorites,
		},
		{ // 5:
			org:                 testdb.Org1,
			channel:             testdb.FacebookChannel,
			contact:             testdb.Cathy,
			text:                "MUTZIG",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Mmmmm... delicious Mutzig. If only they made blue Mutzig! Lastly, what is your name?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Favorites,
		},
		{ // 6:
			org:                 testdb.Org1,
			channel:             testdb.FacebookChannel,
			contact:             testdb.Cathy,
			text:                "Cathy",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Thanks Cathy, we are all done!",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Favorites,
		},
		{ // 7:
			org:                testdb.Org1,
			channel:            testdb.FacebookChannel,
			contact:            testdb.Cathy,
			text:               "noop",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 8:
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "other",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Hey, how are you?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2SingleMessage,
		},
		{ // 9:
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2Favorites,
		},
		{ // 10:
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "green",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Good choice, I like Green too! What is your favorite beer?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2Favorites,
		},
		{ // 11:
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "primus",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Mmmmm... delicious Primus. If only they made green Primus! Lastly, what is your name?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2Favorites,
		},
		{ // 12:
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "george",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Thanks george, we are all done!",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2Favorites,
		},
		{ // 13:
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "blargh",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Hey, how are you?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2SingleMessage,
		},
		{ // 14:
			org:                testdb.Org1,
			channel:            testdb.FacebookChannel,
			contact:            testdb.Bob,
			text:               "ivr",
			expectedVisibility: models.VisibilityVisible,
		},
		{ // 15: stopped contact should be unstopped
			preHook: func() {
				rt.DB.MustExec(`UPDATE contacts_contact SET status = 'S' WHERE id = $1`, testdb.George.ID)
			},
			org:                 testdb.Org1,
			channel:             testdb.FacebookChannel,
			contact:             testdb.George,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Favorites,
		},
		{ // 16: no URN on contact but failed reply created anyway
			org:                 testdb.Org1,
			channel:             testdb.TwilioChannel,
			contact:             testdb.Alexandra,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusFailed,
			expectedFlow:        testdb.Favorites,
		},
		{ // 17: start Fred back in our favorite flow, then make it inactive, will be handled by catch-all
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2Favorites,
		},
		{ // 18:
			preHook: func() {
				rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE WHERE id = $1`, testdb.Org2Favorites.ID)
			},
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "red",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "Hey, how are you?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2SingleMessage,
		},
		{ // 19: start Fred back in our favorites flow to test retries
			preHook: func() {
				rt.DB.MustExec(`UPDATE flows_flow SET is_active = TRUE WHERE id = $1`, testdb.Org2Favorites.ID)
			},
			org:                 testdb.Org2,
			channel:             testdb.Org2Channel,
			contact:             testdb.Org2Contact,
			text:                "start",
			expectedVisibility:  models.VisibilityVisible,
			expectedReplyText:   "What is your favorite color?",
			expectedReplyStatus: models.MsgStatusQueued,
			expectedFlow:        testdb.Org2Favorites,
		},
		{ // 20: deleted contact
			org:     testdb.Org1,
			channel: testdb.TwilioChannel,
			contact: deleted,
			text:    "start",
		},
		{ // 21: blocked contact
			org:                testdb.Org1,
			channel:            testdb.FacebookChannel,
			contact:            blocked,
			text:               "start",
			expectedVisibility: models.VisibilityArchived,
		},
		{ // 22: disabled channel
			org:                testdb.Org1,
			channel:            disabled,
			contact:            testdb.Cathy,
			text:               "start",
			expectedVisibility: models.VisibilityArchived,
		},
	}

	makeMsgTask := func(channel *testdb.Channel, contact *testdb.Contact, text string) handler.Task {
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
		fmt.Sprintf("msgs:%s|10/1", testdb.FacebookChannel.UUID): {1, 1, 1, 1, 1, 1},
		fmt.Sprintf("msgs:%s|10/1", testdb.Org2Channel.UUID):     {1, 1, 1, 1, 1, 1, 1, 1, 1},
	})

	// Fred's sessions should not have a timeout because courier will set them
	assertdb.Query(t, rt.DB, `SELECT count(*) from contacts_contactfire where contact_id = $1 and fire_type = 'T'`, testdb.Org2Contact.ID).Returns(0)

	// force an error by marking our run for fred as complete (our session is still active so this will blow up)
	rt.DB.MustExec(`UPDATE flows_flowrun SET status = 'C', exited_on = NOW() WHERE contact_id = $1`, testdb.Org2Contact.ID)
	handler.QueueTask(rc, testdb.Org2.ID, testdb.Org2Contact.ID, makeMsgTask(testdb.Org2Channel, testdb.Org2Contact, "red"))

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
	rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE where id = $1`, testdb.Org2Favorites.ID)
	models.FlushCache()

	// try to resume now
	handler.QueueTask(rc, testdb.Org2.ID, testdb.Org2Contact.ID, makeMsgTask(testdb.Org2Channel, testdb.Org2Contact, "red"))
	task, _ = tasks.HandlerQueue.Pop(rc)
	assert.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	assert.NoError(t, err)

	// should get our catch all trigger
	assertdb.Query(t, rt.DB, `SELECT text FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' ORDER BY id DESC LIMIT 1`, testdb.Org2Contact.ID).Returns("Hey, how are you?")
	previous := time.Now()

	// and should have failed previous session
	assertdb.Query(t, rt.DB, `SELECT count(*) from flows_flowsession where contact_id = $1 and status = 'F'`, testdb.Org2Contact.ID).Returns(2)

	// trigger should also not start a new session
	handler.QueueTask(rc, testdb.Org2.ID, testdb.Org2Contact.ID, makeMsgTask(testdb.Org2Channel, testdb.Org2Contact, "start"))
	task, _ = tasks.HandlerQueue.Pop(rc)
	err = tasks.Perform(ctx, rt, task)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND created_on > $2`, testdb.Org2Contact.ID, previous).Returns(0)
}
