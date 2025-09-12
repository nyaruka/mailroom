package interrupts_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/interrupts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestInterruptChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// twilio call
	twilioCall := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Dan)

	// vonage call
	vonageCall := testdb.InsertCall(rt, testdb.Org1, testdb.VonageChannel, testdb.Cat)

	sessionUUID1 := testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Ann, models.FlowTypeMessaging, nil, testdb.Favorites)
	sessionUUID2 := testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Cat, models.FlowTypeVoice, vonageCall, testdb.Favorites)
	sessionUUID3 := testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Dan, models.FlowTypeVoice, twilioCall, testdb.Favorites)

	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, vonageCall.ID, sessionUUID2)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, twilioCall.ID, sessionUUID3)

	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "how can we help", nil, models.MsgStatusPending, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Bob, "this failed", nil, models.MsgStatusQueued, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Cat, "no URN", nil, models.MsgStatusPending, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Cat, "no URN", nil, models.MsgStatusErrored, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Cat, "no URN", nil, models.MsgStatusFailed, false)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID1).Returns("W")
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID2).Returns("W")
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID3).Returns("W")

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and failed_reason = 'R' and channel_id = $1`, testdb.VonageChannel.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and channel_id = $1`, testdb.VonageChannel.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and channel_id = $1`, testdb.TwilioChannel.ID).Returns(0)

	// queue and perform a task to interrupt the Twilio channel
	tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &interrupts.InterruptChannelTask{ChannelID: testdb.TwilioChannel.ID}, false)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and channel_id = $1`, testdb.VonageChannel.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and failed_reason = 'R' and channel_id = $1`, testdb.VonageChannel.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and failed_reason = 'R' and channel_id = $1`, testdb.TwilioChannel.ID).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID1).Returns("W")
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID2).Returns("W")
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID3).Returns("I")

	testdb.InsertErroredOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "Hi", 1, time.Now().Add(-time.Hour), false)
	testdb.InsertErroredOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Bob, "Hi", 2, time.Now().Add(-time.Minute), false)
	testdb.InsertErroredOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Bob, "Hi", 2, time.Now().Add(-time.Minute), false)
	testdb.InsertErroredOutgoingMsg(rt, testdb.Org1, testdb.VonageChannel, testdb.Bob, "Hi", 2, time.Now().Add(-time.Minute), true) // high priority

	// just to create courier queues
	cron := &crons.RetrySendingCron{}
	_, err := cron.Run(ctx, rt)
	require.NoError(t, err)

	testsuite.AssertCourierQueues(t, rt, map[string][]int{
		"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0": {1}, // twilio, bulk priority
		"msgs:19012bfd-3ce3-4cae-9bb9-76cf92c73d49|10/0": {2}, // vonage, bulk priority
		"msgs:19012bfd-3ce3-4cae-9bb9-76cf92c73d49|10/1": {1}, // vonage, high priority
	})

	// queue and perform a task to interrupt the Vonage channel
	tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &interrupts.InterruptChannelTask{ChannelID: testdb.VonageChannel.ID}, false)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and failed_reason = 'R' and channel_id = $1`, testdb.VonageChannel.ID).Returns(6)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and channel_id = $1`, testdb.VonageChannel.ID).Returns(7)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' and failed_reason = 'R' and channel_id = $1`, testdb.TwilioChannel.ID).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID1).Returns("W")
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID2).Returns("I")
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID3).Returns("I")

	// vonage queues should be cleared
	testsuite.AssertCourierQueues(t, rt, map[string][]int{
		"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0": {1}, // twilio, bulk priority
	})

}
