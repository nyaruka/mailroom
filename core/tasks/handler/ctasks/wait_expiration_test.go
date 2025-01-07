package ctasks_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestTimedEvents(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetAll)

	// create some keyword triggers
	testdata.InsertKeywordTrigger(rt, testdata.Org1, testdata.Favorites, []string{"start"}, models.MatchOnly, nil, nil, nil)
	testdata.InsertKeywordTrigger(rt, testdata.Org1, testdata.PickANumber, []string{"pick"}, models.MatchOnly, nil, nil, nil)

	tcs := []struct {
		EventType string
		Contact   *testdata.Contact
		Message   string
		Response  string
		Channel   *testdata.Channel
		Org       *testdata.Org
	}{
		// 0: start the flow
		{ctasks.TypeMsgEvent, testdata.Cathy, "start", "What is your favorite color?", testdata.FacebookChannel, testdata.Org1},

		// 1: this expiration does nothing because the times don't match
		{ctasks.TypeWaitExpiration, testdata.Cathy, "bad", "", testdata.FacebookChannel, testdata.Org1},

		// 2: this checks that the flow wasn't expired
		{ctasks.TypeMsgEvent, testdata.Cathy, "red", "Good choice, I like Red too! What is your favorite beer?", testdata.FacebookChannel, testdata.Org1},

		// 3: this expiration will actually take
		{ctasks.TypeWaitExpiration, testdata.Cathy, "good", "", testdata.FacebookChannel, testdata.Org1},

		// 4: we won't get a response as we will be out of the flow
		{ctasks.TypeMsgEvent, testdata.Cathy, "mutzig", "", testdata.FacebookChannel, testdata.Org1},

		// 5: start the parent expiration flow
		{ctasks.TypeMsgEvent, testdata.Cathy, "parent", "Child", testdata.FacebookChannel, testdata.Org1},

		// 6: respond, should bring us out
		{ctasks.TypeMsgEvent, testdata.Cathy, "hi", "Completed", testdata.FacebookChannel, testdata.Org1},

		// 7: expiring our child should be a no op
		{ctasks.TypeWaitExpiration, testdata.Cathy, "child", "", testdata.FacebookChannel, testdata.Org1},

		// 8: respond one last time, should be done
		{ctasks.TypeMsgEvent, testdata.Cathy, "done", "Ended", testdata.FacebookChannel, testdata.Org1},

		// 9: start our favorite flow again
		{ctasks.TypeMsgEvent, testdata.Cathy, "start", "What is your favorite color?", testdata.FacebookChannel, testdata.Org1},

		// 10: timeout on the color question
		{ctasks.TypeWaitTimeout, testdata.Cathy, "", "Sorry you can't participate right now, I'll try again later.", testdata.FacebookChannel, testdata.Org1},

		// 11: start the pick a number flow
		{ctasks.TypeMsgEvent, testdata.Cathy, "pick", "Pick a number between 1-10.", testdata.FacebookChannel, testdata.Org1},

		// 12: try to resume with timeout even tho flow doesn't have one set
		{ctasks.TypeWaitTimeout, testdata.Cathy, "", "", testdata.FacebookChannel, testdata.Org1},
	}

	last := time.Now()
	var sessionID models.SessionID
	var runID models.FlowRunID

	for i, tc := range tcs {
		time.Sleep(50 * time.Millisecond)

		var ctask handler.Task

		if tc.EventType == ctasks.TypeMsgEvent {
			ctask = &ctasks.MsgEventTask{
				ChannelID: tc.Channel.ID,
				MsgID:     models.MsgID(1),
				MsgUUID:   flows.MsgUUID(uuids.NewV4()),
				URN:       tc.Contact.URN,
				URNID:     tc.Contact.URNID,
				Text:      tc.Message,
			}
		} else if tc.EventType == ctasks.TypeWaitExpiration {
			var expiration time.Time

			if tc.Message == "bad" {
				expiration = time.Now()
			} else if tc.Message == "child" {
				rt.DB.Get(&expiration, `SELECT wait_expires_on FROM flows_flowsession WHERE id = $1 AND status != 'W'`, sessionID)
				rt.DB.Get(&runID, `SELECT id FROM flows_flowrun WHERE session_id = $1 AND status NOT IN ('A', 'W')`, sessionID)
			} else {
				expiration = time.Now().Add(time.Hour * 24)
			}

			ctask = ctasks.NewWaitExpiration(sessionID, expiration)

		} else if tc.EventType == ctasks.TypeWaitTimeout {
			timeoutOn := time.Now().Round(time.Millisecond) // so that there's no difference between this and what we read from the db

			// usually courier will set timeout_on after sending the last message
			rt.DB.MustExec(`UPDATE flows_flowsession SET timeout_on = $2 WHERE id = $1`, sessionID, timeoutOn)

			ctask = ctasks.NewWaitTimeout(sessionID, timeoutOn)
		}

		err := handler.QueueTask(rc, tc.Org.ID, tc.Contact.ID, ctask)
		assert.NoError(t, err, "%d: error adding task", i)

		task, err := tasks.HandlerQueue.Pop(rc)
		assert.NoError(t, err, "%d: error popping next task", i)

		err = tasks.Perform(ctx, rt, task)
		assert.NoError(t, err, "%d: error when handling event", i)

		if tc.Response != "" {
			assertdb.Query(t, rt.DB, `SELECT text FROM msgs_msg WHERE contact_id = $1 AND created_on > $2 ORDER BY id DESC LIMIT 1`, tc.Contact.ID, last).
				Returns(tc.Response, "%d: response: mismatch", i)
		}

		err = rt.DB.Get(&sessionID, `SELECT id FROM flows_flowsession WHERE contact_id = $1 ORDER BY created_on DESC LIMIT 1`, tc.Contact.ID)
		assert.NoError(t, err)

		err = rt.DB.Get(&runID, `SELECT id FROM flows_flowrun WHERE contact_id = $1 ORDER BY created_on DESC LIMIT 1`, tc.Contact.ID)
		assert.NoError(t, err)

		last = time.Now()
	}

	// should only have a single waiting session/run with no timeout
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE status = 'W' AND contact_id = $1`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT timeout_on FROM flows_flowsession WHERE status = 'W' AND contact_id = $1`, testdata.Cathy.ID).Returns(nil)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE status = 'W' AND contact_id = $1`, testdata.Cathy.ID).Returns(1)

	// test the case of a run and session no longer being the most recent but somehow still active, expiration should still work
	r, err := rt.DB.QueryContext(ctx, `SELECT id, session_id from flows_flowrun WHERE contact_id = $1 and status = 'I' order by created_on asc limit 1`, testdata.Cathy.ID)
	assert.NoError(t, err)
	defer r.Close()
	r.Next()
	r.Scan(&runID, &sessionID)

	expiration := time.Now()

	// set both to be active (this requires us to disable the status change triggers)
	rt.DB.MustExec(`ALTER TABLE flows_flowrun DISABLE TRIGGER temba_flowrun_on_change`)
	rt.DB.MustExec(`ALTER TABLE flows_flowsession DISABLE TRIGGER temba_flowsession_status_change`)
	rt.DB.MustExec(`UPDATE flows_flowrun SET status = 'W' WHERE id = $1`, runID)
	rt.DB.MustExec(`UPDATE flows_flowsession SET status = 'W', wait_started_on = NOW(), wait_expires_on = $2 WHERE id = $1`, sessionID, expiration)
	rt.DB.MustExec(`ALTER TABLE flows_flowrun ENABLE TRIGGER temba_flowrun_on_change`)
	rt.DB.MustExec(`ALTER TABLE flows_flowsession ENABLE TRIGGER temba_flowsession_status_change`)

	// try to expire the run
	err = handler.QueueTask(rc, testdata.Org1.ID, testdata.Cathy.ID, ctasks.NewWaitExpiration(sessionID, expiration))
	assert.NoError(t, err)

	task, err := tasks.HandlerQueue.Pop(rc)
	assert.NoError(t, err)

	err = tasks.Perform(ctx, rt, task)
	assert.NoError(t, err)
}
