package msgio_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCourierMsg(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey|testsuite.ResetDynamo)

	// create an opt-in and a new contact with an auth token for it
	optInID := testdb.InsertOptIn(rt, testdb.Org1, "Joke Of The Day").ID
	testFred := testdb.InsertContact(rt, testdb.Org1, "", "Fred", "eng", models.ContactStatusActive)
	testdb.InsertContactURN(rt, testdb.Org1, testFred, "tel:+593979123456", 1000, map[string]string{fmt.Sprintf("optin:%d", optInID): "sesame"})

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOptIns)
	require.NoError(t, err)
	require.False(t, oa.Org().Suspended())

	_, fCathy, cathyURNs := testdb.Cathy.Load(rt, oa)
	_, fred, fredURNs := testFred.Load(rt, oa)

	twilio := oa.ChannelByUUID(testdb.TwilioChannel.UUID)
	facebook := oa.ChannelByUUID(testdb.FacebookChannel.UUID)
	flow, _ := oa.FlowByID(testdb.Favorites.ID)
	optIn := oa.OptInByID(optInID)
	cathyURN, _ := cathyURNs[0].Encode(oa)
	fredURN, _ := fredURNs[0].Encode(oa)

	scenes := testsuite.StartSessions(t, rt, oa, []*testdb.Contact{testdb.Cathy}, triggers.NewBuilder(testdb.Favorites.Reference()).Manual().Build())
	session, sprint := scenes[0].Session, scenes[0].Sprint

	msgEvent1 := events.NewMsgCreated(flows.NewMsgOut(
		cathyURN,
		assets.NewChannelReference(testdb.FacebookChannel.UUID, "Facebook"),
		&flows.MsgContent{
			Text:         "Hi there",
			Attachments:  []utils.Attachment{utils.Attachment("image/jpeg:https://dl-foo.com/image.jpg")},
			QuickReplies: []flows.QuickReply{{Text: "yes", Extra: "if you really want"}, {Text: "no"}},
		},
		flows.NewMsgTemplating(
			assets.NewTemplateReference(testdb.ReviveTemplate.UUID, "revive_issue"),
			[]*flows.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
			[]*flows.TemplatingVariable{{Type: "text", Value: "name"}},
		),
		`eng-US`,
		flows.NilUnsendableReason,
	))

	msg1, err := models.NewOutgoingFlowMsg(rt, oa.Org(), facebook, fCathy, flow, msgEvent1, nil)
	require.NoError(t, err)

	// insert to db so that it gets an id and time field values
	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{msg1.Msg})
	require.NoError(t, err)

	msg1.URN = cathyURNs[0]
	msg1.Session = session
	msg1.SprintUUID = sprint.UUID()

	createAndAssertCourierMsg(t, oa, msg1, fmt.Sprintf(`{
		"attachments": [
			"image/jpeg:https://dl-foo.com/image.jpg"
		],
		"channel_uuid": "0f661e8b-ea9d-4bd3-9953-d368340acf91",
		"contact_id": 10000,
		"contact_urn_id": 10000,
		"created_on": %s,
		"flow": {"uuid": "9de3663f-c5c5-4c92-9f45-ecbc09abcc85", "name": "Favorites"},
		"high_priority": false,
		"id": 30001,
		"locale": "eng-US",
		"org_id": 1,
		"origin": "flow",
		"quick_replies": [{"text": "yes", "extra": "if you really want"}, {"text": "no"}],
		"session": {
			"uuid": "%s",
			"status": "W",
			"sprint_uuid": "%s"
        },
		"templating": {
			"template": {"uuid": "9c22b594-fcab-4b29-9bcb-ce4404894a80", "name": "revive_issue"},
			"components": [{"type": "body", "name": "body", "variables": {"1": 0}}],
			"variables": [{"type": "text", "value": "name"}],
			"namespace": "2d40b45c_25cd_4965_9019_f05d0124c5fa",
			"external_id": "eng1",
			"language": "en_US"			
		},
		"text": "Hi there",
		"tps_cost": 2,
		"urn": "tel:+16055741111",
		"uuid": "%s"
	}`, string(jsonx.MustMarshal(msgEvent1.CreatedOn())), session.UUID(), sprint.UUID(), msg1.UUID()))

	// create a priority flow message.. i.e. the session is responding to an incoming message
	fCathy.SetLastSeenOn(time.Date(2023, 4, 20, 10, 15, 0, 0, time.UTC))
	msgEvent2 := events.NewMsgCreated(flows.NewMsgOut(
		cathyURN,
		assets.NewChannelReference(testdb.TwilioChannel.UUID, "Test Channel"),
		&flows.MsgContent{Text: "Hi there"},
		nil,
		i18n.NilLocale,
		flows.NilUnsendableReason,
	))
	in1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "test", models.MsgStatusHandled)
	msg2, err := models.NewOutgoingFlowMsg(rt, oa.Org(), twilio, fCathy, flow, msgEvent2, &models.MsgInRef{ID: in1.ID, ExtID: "EX123"})
	require.NoError(t, err)

	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{msg2.Msg})
	require.NoError(t, err)

	msg2.URN = cathyURNs[0]
	msg2.Session = session
	msg2.SprintUUID = sprint.UUID()

	createAndAssertCourierMsg(t, oa, msg2, fmt.Sprintf(`{
		"channel_uuid": "74729f45-7f29-4868-9dc4-90e491e3c7d8",
		"contact_id": 10000,
		"contact_last_seen_on": "2023-04-20T10:15:00Z",
		"contact_urn_id": 10000,
		"created_on": %s,
		"flow": {"uuid": "9de3663f-c5c5-4c92-9f45-ecbc09abcc85", "name": "Favorites"},
		"response_to_external_id": "EX123",
		"high_priority": true,
		"id": 30003,
		"org_id": 1,
		"origin": "flow",
		"session": {
			"uuid": "%s",
			"status": "W",
			"sprint_uuid": "%s"
        },
		"text": "Hi there",
		"tps_cost": 1,
		"urn": "tel:+16055741111",
		"uuid": "%s"
	}`, string(jsonx.MustMarshal(msgEvent2.CreatedOn())), session.UUID(), sprint.UUID(), msg2.UUID()))

	// try a broadcast message which won't have session and flow fields set and won't be high priority
	bcastID := testdb.InsertBroadcast(rt, testdb.Org1, `eng`, map[i18n.Language]string{`eng`: "Blast"}, nil, models.NilScheduleID, []*testdb.Contact{testFred}, nil)
	msgEvent3 := events.NewMsgCreated(
		flows.NewMsgOut(fredURN, assets.NewChannelReference(testdb.TwilioChannel.UUID, "Test Channel"), &flows.MsgContent{Text: "Blast"}, nil, i18n.NilLocale, flows.NilUnsendableReason),
	)
	msg3, err := models.NewOutgoingBroadcastMsg(rt, oa.Org(), twilio, fred, msgEvent3, &models.Broadcast{ID: bcastID, OptInID: optInID, CreatedByID: testdb.Admin.ID})
	require.NoError(t, err)

	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{msg3.Msg})
	require.NoError(t, err)

	msg3.URN = fredURNs[0]

	createAndAssertCourierMsg(t, oa, msg3, fmt.Sprintf(`{
		"channel_uuid": "74729f45-7f29-4868-9dc4-90e491e3c7d8",
		"contact_id": 30000,
		"contact_urn_id": 30000,
		"created_on": %s,
		"high_priority": false,
		"id": 30004,
		"org_id": 1,
		"origin": "broadcast",
		"text": "Blast",
		"tps_cost": 1,
		"urn": "tel:+593979123456",
		"urn_auth": "sesame",
		"user_id": %d,
		"uuid": "%s"
	}`, string(jsonx.MustMarshal(msgEvent3.CreatedOn())), testdb.Admin.ID, msg3.UUID()))

	optInEvent := events.NewOptInRequested(session.Assets().OptIns().Get(optIn.UUID()), twilio.Reference(), "tel:+16055741111?id=10000")
	msg4 := models.NewOutgoingOptInMsg(rt, testdb.Org1.ID, fCathy, flow, optIn, twilio, optInEvent, &models.MsgInRef{ID: in1.ID, ExtID: "EX123"})
	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{msg4.Msg})
	require.NoError(t, err)

	msg4.URN = cathyURNs[0]
	msg4.Session = session
	msg4.SprintUUID = sprint.UUID()

	createAndAssertCourierMsg(t, oa, msg4, fmt.Sprintf(`{
		"channel_uuid": "74729f45-7f29-4868-9dc4-90e491e3c7d8",
		"contact_id": 10000,
		"contact_last_seen_on": "2023-04-20T10:15:00Z",
		"contact_urn_id": 10000,
		"created_on": %s,
		"flow": {"uuid": "9de3663f-c5c5-4c92-9f45-ecbc09abcc85", "name": "Favorites"},
		"high_priority": true,
		"id": 30005,
		"optin": {
			"id": %d,
			"name": "Joke Of The Day"
		},
		"org_id": 1,
		"origin": "flow",
		"response_to_external_id": "EX123",
		"session": {
			"uuid": "%s",
			"status": "W",
			"sprint_uuid": "%s"
        },
		"text": "",
		"tps_cost": 1,
		"urn": "tel:+16055741111",
		"uuid": "%s"
	}`, string(jsonx.MustMarshal(optInEvent.CreatedOn())), optIn.ID(), session.UUID(), sprint.UUID(), msg4.UUID()))
}

func createAndAssertCourierMsg(t *testing.T, oa *models.OrgAssets, msg *models.MsgOut, expectedJSON string) {
	channel := oa.ChannelByID(msg.ChannelID())

	cmsg3, err := msgio.NewCourierMsg(oa, msg, channel)
	assert.NoError(t, err)

	marshaled := jsonx.MustMarshal(cmsg3)

	test.AssertEqualJSON(t, []byte(expectedJSON), marshaled)
}

func TestQueueCourierMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOrg|models.RefreshChannels)
	require.NoError(t, err)

	_, _, cathyURNs := testdb.Cathy.Load(rt, oa)
	twilio := oa.ChannelByUUID(testdb.TwilioChannel.UUID)

	// noop if no messages provided
	msgio.QueueCourierMessages(vc, oa, testdb.Cathy.ID, twilio, []*models.MsgOut{})
	testsuite.AssertCourierQueues(t, rt, map[string][]int{})

	// queue 3 messages for Cathy..
	sends := []*models.MsgOut{
		{
			Msg: (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa),
			URN: cathyURNs[0],
		},
		{
			Msg: (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa),
			URN: cathyURNs[0],
		},
		{
			Msg: (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy, HighPriority: true}).createMsg(t, rt, oa),
			URN: cathyURNs[0],
		},
	}

	msgio.QueueCourierMessages(vc, oa, testdb.Cathy.ID, twilio, sends)

	testsuite.AssertCourierQueues(t, rt, map[string][]int{
		"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0": {2}, // twilio, bulk priority
		"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1}, // twilio, high priority
	})
}

func TestClearChannelCourierQueue(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOrg|models.RefreshChannels)
	require.NoError(t, err)

	_, _, cathyURNs := testdb.Cathy.Load(rt, oa)
	twilio := oa.ChannelByUUID(testdb.TwilioChannel.UUID)
	vonage := oa.ChannelByUUID(testdb.VonageChannel.UUID)

	// queue 3 Twilio messages for Cathy..
	msgio.QueueCourierMessages(vc, oa, testdb.Cathy.ID, twilio, []*models.MsgOut{
		{
			Msg: (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa),
			URN: cathyURNs[0],
		},
		{
			Msg: (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa),
			URN: cathyURNs[0],
		},
		{
			Msg: (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy, HighPriority: true}).createMsg(t, rt, oa),
			URN: cathyURNs[0],
		},
	})

	// and a Vonage message
	msgio.QueueCourierMessages(vc, oa, testdb.Cathy.ID, vonage, []*models.MsgOut{
		{
			Msg: (&msgSpec{Channel: testdb.VonageChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa),
			URN: cathyURNs[0],
		},
	})

	testsuite.AssertCourierQueues(t, rt, map[string][]int{
		"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0": {2}, // twilio, bulk priority
		"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/1": {1}, // twilio, high priority
		"msgs:19012bfd-3ce3-4cae-9bb9-76cf92c73d49|10/0": {1}, // vonage, bulk priority
	})

	twilioChannel := oa.ChannelByID(testdb.TwilioChannel.ID)
	msgio.ClearCourierQueues(vc, twilioChannel)

	testsuite.AssertCourierQueues(t, rt, map[string][]int{
		"msgs:19012bfd-3ce3-4cae-9bb9-76cf92c73d49|10/0": {1}, // vonage, bulk priority
	})

	vonageChannel := oa.ChannelByID(testdb.VonageChannel.ID)
	msgio.ClearCourierQueues(vc, vonageChannel)
	testsuite.AssertCourierQueues(t, rt, map[string][]int{})

}

func TestPushCourierBatch(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	_, _, cathyURNs := testdb.Cathy.Load(rt, oa)
	channel := oa.ChannelByID(testdb.TwilioChannel.ID)

	msg1 := (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa)
	msg2 := (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa)

	err = msgio.PushCourierBatch(vc, oa, channel, []*models.MsgOut{{Msg: msg1, URN: cathyURNs[0]}, {Msg: msg2, URN: cathyURNs[0]}}, "1636557205.123456")
	require.NoError(t, err)

	// check that channel has been added to active list
	msgsActive, err := redis.Strings(vc.Do("ZRANGE", "msgs:active", 0, -1))
	assert.NoError(t, err)
	assert.Equal(t, []string{"msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10"}, msgsActive)

	// and that msgs were added as single batch to bulk priority (0) queue
	queued, err := redis.ByteSlices(vc.Do("ZRANGE", "msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0", 0, -1))
	assert.NoError(t, err)
	assert.Equal(t, 1, len(queued))

	unmarshaled, err := jsonx.DecodeGeneric(queued[0])
	assert.NoError(t, err)
	assert.Equal(t, 2, len(unmarshaled.([]any)))

	item1ID, _ := unmarshaled.([]any)[0].(map[string]any)["id"].(json.Number).Int64()
	item2ID, _ := unmarshaled.([]any)[1].(map[string]any)["id"].(json.Number).Int64()
	assert.Equal(t, int64(msg1.ID()), item1ID)
	assert.Equal(t, int64(msg2.ID()), item2ID)

	// push another batch in the same epoch second with transaction counter still below limit
	vc.Do("SET", "msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10:tps:1636557205", "5")

	msg3 := (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa)

	err = msgio.PushCourierBatch(vc, oa, channel, []*models.MsgOut{{Msg: msg3, URN: cathyURNs[0]}}, "1636557205.234567")
	require.NoError(t, err)

	queued, err = redis.ByteSlices(vc.Do("ZRANGE", "msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0", 0, -1))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(queued))

	// simulate channel having been throttled
	vc.Do("ZREM", "msgs:active", "msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10")
	vc.Do("SET", "msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10:tps:1636557205", "11")

	msg4 := (&msgSpec{Channel: testdb.TwilioChannel, Contact: testdb.Cathy}).createMsg(t, rt, oa)

	err = msgio.PushCourierBatch(vc, oa, channel, []*models.MsgOut{{Msg: msg4, URN: cathyURNs[0]}}, "1636557205.345678")
	require.NoError(t, err)

	// check that channel has *not* been added to active list
	msgsActive, err = redis.Strings(vc.Do("ZRANGE", "msgs:active", 0, -1))
	assert.NoError(t, err)
	assert.Equal(t, []string{}, msgsActive)

	// but msg was still added to queue
	queued, err = redis.ByteSlices(vc.Do("ZRANGE", "msgs:74729f45-7f29-4868-9dc4-90e491e3c7d8|10/0", 0, -1))
	assert.NoError(t, err)
	assert.Equal(t, 3, len(queued))
}
