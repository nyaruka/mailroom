package ctasks_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimedEvents(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, testsuite.ResetAll)

	// create some keyword triggers
	testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"start"}, models.MatchOnly, nil, nil, nil)
	testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.PickANumber, []string{"pick"}, models.MatchOnly, nil, nil, nil)

	contact := testdb.Cathy

	tcs := []struct {
		eventType        string
		messageIn        string
		expectedResponse string
		expectedFlow     *testdb.Flow
	}{
		// 0: start the flow
		{ctasks.TypeMsgReceived, "start", "What is your favorite color?", testdb.Favorites},

		// 1: this expiration does nothing because the times don't match
		{ctasks.TypeWaitExpired, "bad", "", testdb.Favorites},

		// 2: this checks that the flow wasn't expired
		{ctasks.TypeMsgReceived, "red", "Good choice, I like Red too! What is your favorite beer?", testdb.Favorites},

		// 3: this expiration will actually take
		{ctasks.TypeWaitExpired, "", "", nil},

		// 4: we won't get a response as we will be out of the flow
		{ctasks.TypeMsgReceived, "mutzig", "", nil},

		// 5: start the parent expiration flow
		{ctasks.TypeMsgReceived, "parent", "Child", testdb.ChildTimeoutFlow},

		// 6: expire the child
		{ctasks.TypeWaitExpired, "", "Expired", testdb.ParentTimeoutFlow},

		// 7: expire the parent
		{ctasks.TypeWaitExpired, "", "", nil},

		// 8: start the parent expiration flow again
		{ctasks.TypeMsgReceived, "parent", "Child", testdb.ChildTimeoutFlow},

		// 9: respond to end normally
		{ctasks.TypeMsgReceived, "done", "Completed", testdb.ParentTimeoutFlow},

		// 10: start our favorite flow again
		{ctasks.TypeMsgReceived, "start", "What is your favorite color?", testdb.Favorites},

		// 11: timeout on the color question with bad sprint UUID
		{ctasks.TypeWaitTimeout, "bad", "", testdb.Favorites},

		// 12: timeout on the color question
		{ctasks.TypeWaitTimeout, "", "Sorry you can't participate right now, I'll try again later.", nil},

		// 13: start the pick a number flow
		{ctasks.TypeMsgReceived, "pick", "Pick a number between 1-10.", testdb.PickANumber},

		// 14: try to resume with timeout even tho flow doesn't have one set
		{ctasks.TypeWaitTimeout, "", "", testdb.PickANumber},
	}

	last := time.Now()
	var sessionUUID flows.SessionUUID
	var sprintUUID flows.SprintUUID

	for i, tc := range tcs {
		time.Sleep(50 * time.Millisecond)

		var ctask realtime.Task
		taskSprintUUID := sprintUUID
		if tc.messageIn == "bad" {
			taskSprintUUID = flows.SprintUUID(uuids.NewV4())
		}

		if tc.eventType == ctasks.TypeMsgReceived {
			ctask = &ctasks.MsgReceivedTask{
				ChannelID: testdb.FacebookChannel.ID,
				MsgID:     models.MsgID(1),
				MsgUUID:   flows.NewEventUUID(),
				URN:       contact.URN,
				URNID:     contact.URNID,
				Text:      tc.messageIn,
			}
		} else if tc.eventType == ctasks.TypeWaitExpired {
			ctask = &ctasks.WaitExpiredTask{SessionUUID: sessionUUID, SprintUUID: taskSprintUUID}
		} else if tc.eventType == ctasks.TypeWaitTimeout {
			ctask = &ctasks.WaitTimeoutTask{SessionUUID: sessionUUID, SprintUUID: taskSprintUUID}
		}

		err := realtime.QueueTask(ctx, rt, testdb.Org1.ID, testdb.Cathy.ID, ctask)
		assert.NoError(t, err, "%d: error adding task", i)

		task, err := rt.Queues.Realtime.Pop(ctx, vc)
		assert.NoError(t, err, "%d: error popping next task", i)

		err = tasks.Perform(ctx, rt, task)
		assert.NoError(t, err, "%d: error when handling event", i)

		if tc.expectedResponse != "" {
			assertdb.Query(t, rt.DB, `SELECT text FROM msgs_msg WHERE contact_id = $1 AND created_on > $2 ORDER BY id DESC LIMIT 1`, contact.ID, last).
				Returns(tc.expectedResponse, "%d: response: mismatch", i)
		}
		if tc.expectedFlow != nil {
			// check current_flow is set correctly on the contact
			assertdb.Query(t, rt.DB, `SELECT current_flow_id FROM contacts_contact WHERE id = $1`, contact.ID).Returns(int64(tc.expectedFlow.ID), "%d: flow: mismatch", i)

			// check that we have a waiting session
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'W' AND last_sprint_uuid IS NOT NULL`, contact.ID).Returns(1, "%d: session: mismatch", i)
		} else {
			assertdb.Query(t, rt.DB, `SELECT current_flow_id FROM contacts_contact WHERE id = $1`, contact.ID).Returns(nil, "%d: flow: mismatch", i)
		}

		err = rt.DB.Get(&sessionUUID, `SELECT uuid FROM flows_flowsession WHERE contact_id = $1 ORDER BY id DESC LIMIT 1`, contact.ID)
		require.NoError(t, err)
		err = rt.DB.Get(&sprintUUID, `SELECT last_sprint_uuid FROM flows_flowsession WHERE contact_id = $1 ORDER BY id DESC LIMIT 1`, contact.ID)
		require.NoError(t, err)

		last = time.Now()
	}

	// should only have a single waiting session/run with no timeout
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE status = 'W' AND contact_id = $1`, testdb.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE status = 'W' AND contact_id = $1`, testdb.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'E'`, testdb.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1 AND fire_type = 'T'`, testdb.Cathy.ID).Returns(0)
}
