package msgio_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/msgio"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type msgSpec struct {
	UUID         events.EventUUID
	Channel      *testdb.Channel
	Contact      *testdb.Contact
	Failed       bool
	HighPriority bool
}

func (m *msgSpec) createMsg(t *testing.T, rt *runtime.Runtime, oa *models.OrgAssets) *models.Msg {
	if m.UUID == "" {
		m.UUID = events.NewEventUUID()
	}

	status := models.MsgStatusQueued
	if m.Failed {
		status = models.MsgStatusFailed
	}

	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, m.UUID, m.Channel, m.Contact, "Hello", nil, status, m.HighPriority)
	msgs, err := models.GetMessagesByUUID(context.Background(), rt.DB, testdb.Org1.ID, models.DirectionOut, []events.EventUUID{m.UUID})
	require.NoError(t, err)

	msg := msgs[0]

	// use the channel instances in org assets so they're shared between msg instances
	if msg.ChannelID() != models.NilChannelID {
		msg.SetChannel(oa.ChannelByID(msg.ChannelID()))
	}
	return msg
}

func TestQueueMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	mockFCM := rt.FCM.(*testsuite.MockFCMClient)

	// create some Andoid channels
	androidChannel1 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID1"})
	androidChannel2 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"})
	testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"})

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
					Contact: testdb.Ann,
				},
				{
					Channel: androidChannel1,
					Contact: testdb.Bob,
				},
				{
					Channel: testdb.TwilioChannel,
					Contact: testdb.Ann,
				},
				{
					Channel:      testdb.TwilioChannel,
					Contact:      testdb.Bob,
					HighPriority: true,
				},
			},
			QueueSizes: map[string][]int{
				"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0": {2}, // 2 default priority messages for Ann
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
					Contact: testdb.Ann,
				},
				{
					Channel: androidChannel2,
					Contact: testdb.Bob,
				},
				{
					Channel: androidChannel1,
					Contact: testdb.Ann,
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
					Contact: testdb.Ann,
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
					Contact: testdb.Ann,
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
			contact, _, _ := ms.Contact.Load(t, rt, oa)
			msgs[i] = &models.MsgOut{Msg: ms.createMsg(t, rt, oa), Contact: contact}
		}

		vc.Do("FLUSHDB")
		mockFCM.Messages = nil

		msgio.QueueMessages(ctx, rt, msgs)

		testsuite.AssertCourierQueues(t, rt, tc.QueueSizes, "courier queue sizes mismatch in '%s'", tc.Description)

		// check the FCM tokens that were synced
		actualTokens := make([]string, len(mockFCM.Messages))
		for i := range mockFCM.Messages {
			actualTokens[i] = mockFCM.Messages[i].Token
		}

		assert.ElementsMatch(t, tc.FCMTokensSynced, actualTokens, "FCM tokens mismatch in '%s'", tc.Description)

		assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'I'`).Returns(tc.UnqueuedMsgs, `initializing messages mismatch in '%s'`, tc.Description)
	}
}

func TestQueueMessagesOutsideWindow(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	wac1 := testdb.InsertChannel(t, rt, testdb.Org1, models.ChannelTypeWhatsAppCloud, "WhatsApp 1", "12345", []string{"whatsapp"}, "SR", map[string]any{})
	wac2 := testdb.InsertChannel(t, rt, testdb.Org1, models.ChannelTypeWhatsAppCloud, "WhatsApp 2", "23456", []string{"whatsapp"}, "SR", map[string]any{})
	d3c := testdb.InsertChannel(t, rt, testdb.Org1, models.ChannelTypeD360Cloud, "360 Dialog", "34567", []string{"whatsapp"}, "SR", map[string]any{})

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	flow, _ := oa.FlowByID(testdb.Favorites.ID)
	now := dates.Now()

	// contacts with whatsapp URNs and different last seen on values
	neverSeen := testdb.InsertContact(t, rt, testdb.Org1, "f4d8f6b4-1fbf-4c05-8a3e-a7f1d3e9e4c1", "Never", "eng", models.ContactStatusActive)
	neverSeen.URN = "whatsapp:16055744444"
	neverSeen.URNID = testdb.InsertContactURN(t, rt, testdb.Org1, neverSeen, neverSeen.URN, 1000, nil)
	seenLongAgo := testdb.InsertContact(t, rt, testdb.Org1, "9f0a5c7a-3a65-4c8d-9b1c-0d7b4b8f2a11", "Long Ago", "eng", models.ContactStatusActive)
	seenLongAgo.URN = "whatsapp:16055745555"
	seenLongAgo.URNID = testdb.InsertContactURN(t, rt, testdb.Org1, seenLongAgo, seenLongAgo.URN, 1000, nil)
	seenRecently := testdb.InsertContact(t, rt, testdb.Org1, "2a5e6c8f-9a12-4b0e-8c3d-5f7a1b2c3d44", "Recent", "eng", models.ContactStatusActive)
	seenRecently.URN = "whatsapp:16055746666"
	seenRecently.URNID = testdb.InsertContactURN(t, rt, testdb.Org1, seenRecently, seenRecently.URN, 1000, nil)
	testdb.InsertContactURN(t, rt, testdb.Org1, seenRecently, "tel:+16055746666", 999, nil)

	// the recently seen contact messaged us on the second WhatsApp channel 2 hours ago and on the first 25 hours ago
	in1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, testdb.MsgUUIDAt(now.Add(-2*time.Hour)), wac2, seenRecently, "hi", models.MsgStatusHandled, "")
	testdb.InsertIncomingMsg(t, rt, testdb.Org1, testdb.MsgUUIDAt(now.Add(-25*time.Hour)), wac1, seenRecently, "hi", models.MsgStatusHandled, "")

	never, _, neverURNs := neverSeen.Load(t, rt, oa)
	longAgo, _, longAgoURNs := seenLongAgo.Load(t, rt, oa)
	recent, _, recentURNs := seenRecently.Load(t, rt, oa)
	require.NoError(t, longAgo.UpdateLastSeenOn(ctx, rt.DB, now.Add(-25*time.Hour)))
	require.NoError(t, recent.UpdateLastSeenOn(ctx, rt.DB, now.Add(-2*time.Hour)))

	templating := core.NewMsgTemplating(
		assets.NewTemplateReference(testdb.ReviveTemplate.UUID, "revive_issue"),
		[]*core.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
		[]*core.TemplatingVariable{{Type: "text", Value: "name"}},
	)

	newFlowMsg := func(ch *testdb.Channel, contact *models.Contact, urn *models.ContactURN, tpl *core.MsgTemplating, replyTo *models.MsgInRef) *models.MsgOut {
		event := events.NewMsgCreated(core.NewMsgOut(urn.Identity, assets.NewChannelReference(ch.UUID, ""), &core.MsgContent{Text: "Hi"}, tpl, i18n.NilLocale, ""), "", "")
		mo, err := models.NewOutgoingFlowMsg(rt, oa.Org(), oa.ChannelByID(ch.ID), contact, flow, event, replyTo)
		require.NoError(t, err)
		mo.URN = urn
		return mo
	}

	chatEvent := events.NewMsgCreated(core.NewMsgOut(neverURNs[0].Identity, assets.NewChannelReference(wac1.UUID, ""), &core.MsgContent{Text: "Hi"}, nil, i18n.NilLocale, ""), "", "")
	chatMsg, err := models.NewOutgoingChatMsg(rt, oa.Org(), oa.ChannelByID(wac1.ID), never, chatEvent, testdb.Admin.ID)
	require.NoError(t, err)
	chatMsg.URN = neverURNs[0]

	tests := []struct {
		description string
		msg         *models.MsgOut
		expected    bool
	}{
		{"contact never seen", newFlowMsg(wac1, never, neverURNs[0], nil, nil), true},
		{"contact never seen, D3C channel", newFlowMsg(d3c, never, neverURNs[0], nil, nil), true},
		{"contact last seen 25 hours ago", newFlowMsg(wac1, longAgo, longAgoURNs[0], nil, nil), true},
		{"contact last seen 2 hours ago, incoming on this channel 2 hours ago", newFlowMsg(wac2, recent, recentURNs[0], nil, nil), false},
		{"contact last seen 2 hours ago, incoming on this channel 25 hours ago", newFlowMsg(wac1, recent, recentURNs[0], nil, nil), true},
		{"contact last seen 2 hours ago, no incoming on this channel", newFlowMsg(d3c, recent, recentURNs[0], nil, nil), true},
		{"reply to incoming message", newFlowMsg(wac1, never, neverURNs[0], nil, &models.MsgInRef{UUID: in1.UUID, ExtID: "EX123"}), false},
		{"templated message", newFlowMsg(wac1, never, neverURNs[0], templating, nil), false},
		{"chat message", chatMsg, false},
		{"non-WhatsApp channel", newFlowMsg(testdb.TwilioChannel, recent, recentURNs[1], nil, nil), false},
	}

	msgs := make([]*models.MsgOut, len(tests))
	for i, tc := range tests {
		msgs[i] = tc.msg
	}
	msgModels := make([]*models.Msg, len(msgs))
	for i, m := range msgs {
		msgModels[i] = m.Msg
	}
	require.NoError(t, models.InsertMessages(ctx, rt.DB, msgModels))

	vc.Do("FLUSHDB")

	msgio.QueueMessages(ctx, rt, msgs)

	// read back everything queued to courier, keyed by message UUID
	queued := make(map[events.EventUUID]*msgio.Msg)
	queueKeys, err := valkey.Strings(vc.Do("KEYS", "msgs:????????-*"))
	require.NoError(t, err)
	for _, key := range queueKeys {
		batches, err := valkey.ByteSlices(vc.Do("ZRANGE", key, 0, -1))
		require.NoError(t, err)
		for _, batchJSON := range batches {
			var batch []*msgio.Msg
			require.NoError(t, json.Unmarshal(batchJSON, &batch))
			for _, m := range batch {
				queued[m.UUID] = m
			}
		}
	}

	for _, tc := range tests {
		assert.Equal(t, tc.expected, tc.msg.OutsideWindow, "outside window mismatch for '%s'", tc.description)
		require.Contains(t, queued, tc.msg.UUID(), "message not queued for '%s'", tc.description)
		assert.Equal(t, tc.expected, queued[tc.msg.UUID()].OutsideWindow, "queued outside window mismatch for '%s'", tc.description)
	}
}
