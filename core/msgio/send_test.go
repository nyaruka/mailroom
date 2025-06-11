package msgio_test

import (
	"context"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type msgSpec struct {
	Channel      *testdb.Channel
	Contact      *testdb.Contact
	Failed       bool
	HighPriority bool
}

func (m *msgSpec) createMsg(t *testing.T, rt *runtime.Runtime, oa *models.OrgAssets) *models.Msg {
	status := models.MsgStatusQueued
	if m.Failed {
		status = models.MsgStatusFailed
	}

	msgID := testdb.InsertOutgoingMsg(rt, testdb.Org1, m.Channel, m.Contact, "Hello", nil, status, m.HighPriority).ID
	msgs, err := models.GetMessagesByID(context.Background(), rt.DB, testdb.Org1.ID, models.DirectionOut, []models.MsgID{msgID})
	require.NoError(t, err)

	msg := msgs[0]

	// use the channel instances in org assets so they're shared between msg instances
	if msg.ChannelID() != models.NilChannelID {
		msg.SetChannel(oa.ChannelByID(msg.ChannelID()))
	}
	return msg
}

func TestQueueMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetData)

	mockFCM := rt.FCM.(*testsuite.MockFCMClient)

	// create some Andoid channels
	androidChannel1 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID1"})
	androidChannel2 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"})
	testdb.InsertChannel(rt, testdb.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"})

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	tests := []struct {
		Description     string
		Msgs            []msgSpec
		QueueSizes      map[string][]int
		FCMTokensSynced []string
		UnqueuedMsgs    int
	}{
		{
			Description:     "no messages",
			Msgs:            []msgSpec{},
			QueueSizes:      map[string][]int{},
			FCMTokensSynced: []string{},
			UnqueuedMsgs:    0,
		},
		{
			Description: "2 messages for Courier, and 1 Android",
			Msgs: []msgSpec{
				{
					Channel: testdb.TwilioChannel,
					Contact: testdb.Cathy,
				},
				{
					Channel: androidChannel1,
					Contact: testdb.Bob,
				},
				{
					Channel: testdb.TwilioChannel,
					Contact: testdb.Cathy,
				},
				{
					Channel:      testdb.TwilioChannel,
					Contact:      testdb.Bob,
					HighPriority: true,
				},
			},
			QueueSizes: map[string][]int{
				"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0": {2}, // 2 default priority messages for Cathy
				"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1}, // 1 high priority message for Bob
			},
			FCMTokensSynced: []string{"FCMID1"},
			UnqueuedMsgs:    0,
		},
		{
			Description: "each Android channel synced once",
			Msgs: []msgSpec{
				{
					Channel: androidChannel1,
					Contact: testdb.Cathy,
				},
				{
					Channel: androidChannel2,
					Contact: testdb.Bob,
				},
				{
					Channel: androidChannel1,
					Contact: testdb.Cathy,
				},
			},
			QueueSizes:      map[string][]int{},
			FCMTokensSynced: []string{"FCMID1", "FCMID2"},
			UnqueuedMsgs:    0,
		},
		{
			Description: "messages with FAILED status ignored",
			Msgs: []msgSpec{
				{
					Channel: testdb.TwilioChannel,
					Contact: testdb.Cathy,
					Failed:  true,
				},
			},
			QueueSizes:      map[string][]int{},
			FCMTokensSynced: []string{},
			UnqueuedMsgs:    0,
		},
		{
			Description: "messages without channels set to PENDING",
			Msgs: []msgSpec{
				{
					Channel: nil,
					Contact: testdb.Cathy,
				},
			},
			QueueSizes:      map[string][]int{},
			FCMTokensSynced: []string{},
			UnqueuedMsgs:    1,
		},
	}

	for _, tc := range tests {
		msgs := make([]*models.MsgOut, len(tc.Msgs))
		for i, ms := range tc.Msgs {
			msgs[i] = &models.MsgOut{Msg: ms.createMsg(t, rt, oa)}
		}

		rc.Do("FLUSHDB")
		mockFCM.Messages = nil

		msgio.QueueMessages(ctx, rt, msgs)

		testsuite.AssertCourierQueues(t, tc.QueueSizes, "courier queue sizes mismatch in '%s'", tc.Description)

		// check the FCM tokens that were synced
		actualTokens := make([]string, len(mockFCM.Messages))
		for i := range mockFCM.Messages {
			actualTokens[i] = mockFCM.Messages[i].Token
		}

		assert.ElementsMatch(t, tc.FCMTokensSynced, actualTokens, "FCM tokens mismatch in '%s'", tc.Description)

		assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'I'`).Returns(tc.UnqueuedMsgs, `initializing messages mismatch in '%s'`, tc.Description)
	}
}
