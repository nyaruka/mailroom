package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutgoingFlowMsg(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	blake := testdb.InsertContact(t, rt, testdb.Org1, "79b94a23-6d13-43f4-95fe-c733ee457857", "Blake", i18n.NilLanguage, models.ContactStatusBlocked)
	blakeURNID := testdb.InsertContactURN(t, rt, testdb.Org1, blake, "tel:+250700000007", 1, nil)

	tcs := []struct {
		Channel      *testdb.Channel
		Contact      *testdb.Contact
		URN          urns.URN
		Content      *flows.MsgContent
		Templating   *flows.MsgTemplating
		Locale       i18n.Locale
		Unsendable   flows.UnsendableReason
		Flow         *testdb.Flow
		ResponseTo   *models.MsgInRef
		SuspendedOrg bool

		ExpectedURNID        models.URNID
		ExpectedStatus       models.MsgStatus
		ExpectedFailedReason models.MsgFailedReason
		ExpectedMsgCount     int
		ExpectedPriority     bool
	}{
		{ // 0
			Channel: testdb.TwilioChannel,
			Contact: testdb.Ann,
			URN:     "tel:+16055741111",
			Content: &flows.MsgContent{
				Text: "test outgoing",
				QuickReplies: []flows.QuickReply{
					{Type: "text", Text: "yes", Extra: "if you want"},
					{Type: "text", Text: "no"},
				},
			},
			Templating: flows.NewMsgTemplating(
				assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
				[]*flows.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
				[]*flows.TemplatingVariable{{Type: "text", Value: "name"}},
			),
			Locale:               "eng-US",
			Flow:                 testdb.SingleMessage,
			ExpectedURNID:        testdb.Ann.URNID,
			ExpectedStatus:       models.MsgStatusQueued,
			ExpectedFailedReason: models.NilMsgFailedReason,
			ExpectedMsgCount:     1,
			ExpectedPriority:     false,
		},
		{ // 1
			Channel:              testdb.TwilioChannel,
			Contact:              testdb.Ann,
			URN:                  "tel:+16055741111",
			Content:              &flows.MsgContent{Text: "test outgoing", Attachments: []utils.Attachment{utils.Attachment("image/jpeg:https://dl-foo.com/image.jpg")}},
			Flow:                 testdb.Favorites,
			ExpectedURNID:        testdb.Ann.URNID,
			ExpectedStatus:       models.MsgStatusQueued,
			ExpectedFailedReason: models.NilMsgFailedReason,
			ExpectedMsgCount:     2,
			ExpectedPriority:     false,
		},
		{ // 2: no destination
			Channel:              nil,
			Contact:              testdb.Ann,
			URN:                  urns.NilURN,
			Content:              &flows.MsgContent{Text: "hello"},
			Unsendable:           flows.UnsendableReasonNoRoute,
			Flow:                 testdb.Favorites,
			ExpectedURNID:        models.URNID(0),
			ExpectedStatus:       models.MsgStatusFailed,
			ExpectedFailedReason: models.MsgFailedNoDestination,
			ExpectedMsgCount:     1,
			ExpectedPriority:     false,
		},
		{ // 3: blocked contact
			Channel:              testdb.TwilioChannel,
			Contact:              blake,
			URN:                  "tel:+250700000007",
			Content:              &flows.MsgContent{Text: "hello"},
			Unsendable:           flows.UnsendableReasonContactBlocked,
			Flow:                 testdb.Favorites,
			ExpectedURNID:        blakeURNID,
			ExpectedStatus:       models.MsgStatusFailed,
			ExpectedFailedReason: models.MsgFailedContact,
			ExpectedMsgCount:     1,
			ExpectedPriority:     false,
		},
	}

	now := time.Now()

	for i, tc := range tcs {
		rt.DB.MustExec(`UPDATE orgs_org SET is_suspended = $1 WHERE id = $2`, tc.SuspendedOrg, testdb.Org1.ID)

		oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOrg|models.RefreshFlows)
		require.NoError(t, err)

		var ch *models.Channel
		var chRef *assets.ChannelReference
		expectedChannelID := models.NilChannelID
		if tc.Channel != nil {
			ch = oa.ChannelByUUID(tc.Channel.UUID)
			chRef = ch.Reference()
			expectedChannelID = ch.ID()
		}

		flow, err := oa.FlowByID(tc.Flow.ID)
		require.NoError(t, err)

		mc, _, _ := tc.Contact.Load(t, rt, oa)
		msgEvent := events.NewMsgCreated(flows.NewMsgOut(tc.URN, chRef, tc.Content, tc.Templating, tc.Locale, tc.Unsendable), "", "")
		msg, err := models.NewOutgoingFlowMsg(rt, oa.Org(), ch, mc, flow, msgEvent, tc.ResponseTo)
		assert.NoError(t, err)

		expectedAttachments := tc.Content.Attachments
		if expectedAttachments == nil {
			expectedAttachments = []utils.Attachment{}
		}
		expectedQuickReplies := tc.Content.QuickReplies
		if expectedQuickReplies == nil {
			expectedQuickReplies = []flows.QuickReply{}
		}

		err = models.InsertMessages(ctx, rt.DB, []*models.Msg{msg.Msg})
		assert.NoError(t, err)
		assert.Equal(t, oa.OrgID(), msg.OrgID())
		assert.Equal(t, tc.Content.Text, msg.Text(), "%d: text mismatch", i)
		assert.Equal(t, models.MsgTypeText, msg.Type(), "%d: type mismatch", i)
		assert.Equal(t, expectedAttachments, msg.Attachments(), "%d: attachments mismatch", i)
		assert.Equal(t, expectedQuickReplies, msg.QuickReplies(), "%d: quick replies mismatch", i)
		assert.Equal(t, tc.Locale, msg.Locale(), "%d: locale mismatch", i)

		if tc.Templating != nil {
			assert.Equal(t, tc.Templating, msg.Templating().MsgTemplating, "%d: templating mismatch", i)
		} else {
			assert.Nil(t, msg.Templating(), "%d: templating should be nil", i)
		}

		assert.Equal(t, tc.Contact.ID, msg.ContactID(), "%d: contact id mismatch", i)
		assert.Equal(t, expectedChannelID, msg.ChannelID(), "%d: channel id mismatch", i)
		assert.Equal(t, tc.ExpectedURNID, msg.ContactURNID(), "%d: urn id mismatch", i)
		assert.Equal(t, tc.Flow.ID, msg.FlowID(), "%d: flow id mismatch", i)

		assert.Equal(t, tc.ExpectedStatus, msg.Status(), "%d: status mismatch", i)
		assert.Equal(t, tc.ExpectedFailedReason, msg.FailedReason(), "%d: failed reason mismatch", i)
		assert.Equal(t, tc.ExpectedMsgCount, msg.MsgCount(), "%d: msg count mismatch", i)
		assert.True(t, msg.ID() > 0)
		assert.True(t, msg.CreatedOn().After(now))
		assert.True(t, msg.ModifiedOn().After(now))
	}

	// check nil failed reasons are saved as NULLs
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE failed_reason IS NOT NULL`).Returns(2)

	// check writing of quick replies
	assertdb.Query(t, rt.DB, `SELECT quickreplies::text FROM msgs_msg WHERE id = 30000`).Returns(`[{"text": "yes", "type": "text", "extra": "if you want"}, {"text": "no", "type": "text"}]`)
	assertdb.Query(t, rt.DB, `SELECT quickreplies FROM msgs_msg WHERE id = 30001`).Returns(nil)
}

func TestGetMessagesByUUID(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	msgIn1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-d4be-76c7-8a5c-a12caae7aa87", testdb.TwilioChannel, testdb.Ann, "in 1", models.MsgStatusHandled)
	msgOut1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "out 1", []utils.Attachment{"image/jpeg:hi.jpg"}, models.MsgStatusSent, false)
	msgOut2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "out 2", nil, models.MsgStatusSent, false)
	msgOut3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org2, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.Org2Channel, testdb.Org2Contact, "out 3", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "hi 3", nil, models.MsgStatusSent, false)

	uuids := []flows.EventUUID{msgIn1.UUID, msgOut1.UUID, msgOut2.UUID, msgOut3.UUID}

	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, uuids)

	// should only return the outgoing messages for this org
	require.NoError(t, err)
	assert.Equal(t, 2, len(msgs))
	assert.Equal(t, "out 1", msgs[0].Text())
	assert.Equal(t, []utils.Attachment{"image/jpeg:hi.jpg"}, msgs[0].Attachments())
	assert.Equal(t, "out 2", msgs[1].Text())

	msgs, err = models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionIn, uuids)

	// should only return the incoming message for this org
	require.NoError(t, err)
	assert.Equal(t, 1, len(msgs))
	assert.Equal(t, "in 1", msgs[0].Text())
}

func TestResendMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)
	out2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Bob, "hi", nil, models.MsgStatusFailed, false)

	// failed message with no channel
	out3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", nil, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)

	// failed message with no URN
	out4 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL, failed_reason = 'D' WHERE id = $1`, out4.ID)

	// failed message with URN which we no longer have a channel for
	out5 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb96-3c4c-72f2-bacc-4b6ae4c592b3", nil, testdb.Cat, "hi", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET failed_reason = 'E' WHERE id = $1`, out5.ID)
	rt.DB.MustExec(`UPDATE contacts_contacturn SET scheme = 'viber', path = '1234', identity = 'viber:1234' WHERE id = $1`, testdb.Cat.URNID)

	// other failed message not included in set to resend
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb98-3637-778d-9dfc-0ab85c950d7c", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)

	// give Bob's URN an affinity for the Vonage channel
	rt.DB.MustExec(`UPDATE contacts_contacturn SET channel_id = $1 WHERE id = $2`, testdb.VonageChannel.ID, testdb.Bob.URNID)

	uuids := []flows.EventUUID{out1.UUID, out2.UUID, out3.UUID, out4.UUID, out5.UUID}
	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, uuids)
	require.NoError(t, err)

	// resend both msgs
	resent, err := models.PrepareMessagesForResend(ctx, rt, oa, msgs)
	require.NoError(t, err)

	assert.Len(t, resent, 3) // only #1, #2 and #3 can be resent

	// both messages should now have a channel and be marked for resending
	assert.True(t, resent[0].IsResend)
	assert.Equal(t, testdb.TwilioChannel.ID, resent[0].ChannelID())
	assert.NotNil(t, resent[0].URN)
	assert.True(t, resent[1].IsResend)
	assert.Equal(t, testdb.VonageChannel.ID, resent[1].ChannelID()) // channel changed
	assert.NotNil(t, resent[1].URN)
	assert.True(t, resent[2].IsResend)
	assert.Equal(t, testdb.TwilioChannel.ID, resent[2].ChannelID()) // channel added
	assert.NotNil(t, resent[2].URN)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'Q' AND sent_on IS NULL`).Returns(3)

	assertdb.Query(t, rt.DB, `SELECT status, failed_reason FROM msgs_msg WHERE id = $1`, out4.ID).Columns(map[string]any{"status": "F", "failed_reason": "D"})
	assertdb.Query(t, rt.DB, `SELECT status, failed_reason FROM msgs_msg WHERE id = $1`, out5.ID).Columns(map[string]any{"status": "F", "failed_reason": "D"})
}

func TestFailMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusPending, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Bob, "hi", nil, models.MsgStatusErrored, false)
	out3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb96-3c4c-72f2-bacc-4b6ae4c592b3", testdb.TwilioChannel, testdb.Cat, "hi", nil, models.MsgStatusQueued, false)

	now := dates.Now()

	// fail the msgs
	err := models.FailChannelMessages(ctx, rt.DB.DB, testdb.Org1.ID, testdb.TwilioChannel.ID, models.MsgFailedChannelRemoved)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' AND modified_on > $1`, now).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' AND failed_reason = 'R' AND modified_on > $1`, now).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT status, failed_reason FROM msgs_msg WHERE id = $1`, out3.ID).Columns(map[string]any{"status": "F", "failed_reason": nil})
}

func TestDeleteMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	in1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusHandled)
	in1.Label(rt, testdb.ReportingLabel, testdb.TestingLabel)
	in2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "bye", models.MsgStatusHandled)
	in2.Label(rt, testdb.ReportingLabel, testdb.TestingLabel)
	in3 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "3", models.MsgStatusHandled)
	in4 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.TwilioChannel, testdb.Ann, "4", models.MsgStatusHandled)
	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb96-3c4c-72f2-bacc-4b6ae4c592b3", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusSent, false)

	tx := rt.DB.MustBegin()

	err := models.DeleteMessages(ctx, tx, testdb.Org1.ID, []flows.EventUUID{in1.UUID}, models.VisibilityDeletedBySender)
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, `SELECT visibility, text FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "X", "text": ""})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, in1.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, in2.ID).Returns(2) // unchanged

	tx = rt.DB.MustBegin()

	err = models.DeleteMessages(ctx, tx, testdb.Org1.ID, []flows.EventUUID{in3.UUID, in4.UUID}, models.VisibilityDeletedByUser)
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, `SELECT visibility, text FROM msgs_msg WHERE id = $1`, in3.ID).Columns(map[string]any{"visibility": "D", "text": ""})
	assertdb.Query(t, rt.DB, `SELECT visibility, text FROM msgs_msg WHERE id = $1`, in4.ID).Columns(map[string]any{"visibility": "D", "text": ""})

	tx = rt.DB.MustBegin()

	// trying to delete an outgoing message is a noop
	err = models.DeleteMessages(ctx, tx, testdb.Org1.ID, []flows.EventUUID{out1.UUID}, models.VisibilityDeletedBySender)
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, `SELECT visibility, text FROM msgs_msg WHERE id = $1`, out1.ID).Columns(map[string]any{"visibility": "V", "text": "hi"})
}

func TestGetMsgRepetitions(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)
	defer dates.SetNowFunc(time.Now)

	dates.SetNowFunc(dates.NewFixedNow(time.Date(2021, 11, 18, 12, 13, 3, 234567, time.UTC)))

	msg1 := &flows.MsgContent{Text: "foo"}
	msg2 := &flows.MsgContent{Text: "FOO"}
	msg3 := &flows.MsgContent{Text: "bar"}
	msg4 := &flows.MsgContent{Text: "foo"}

	assertRepetitions := func(contactID models.ContactID, m *flows.MsgContent, expected int) {
		count, err := models.GetMsgRepetitions(rt.VK, contactID, m)
		require.NoError(t, err)
		assert.Equal(t, expected, count)
	}

	for i := range 20 {
		assertRepetitions(testdb.Ann.ID, msg1, i+1)
	}
	for i := range 10 {
		assertRepetitions(testdb.Ann.ID, msg2, i+21)
	}
	for i := range 5 {
		assertRepetitions(testdb.Ann.ID, msg3, i+1)
	}
	for i := range 5 {
		assertRepetitions(testdb.Cat.ID, msg4, i+1)
	}
	assertvk.HGetAll(t, vc, "msg_repetitions:2021-11-18T12:15", map[string]string{"10000|foo": "30", "10000|bar": "5", "10002|foo": "5"})
}

func TestNormalizeAttachment(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	rt.Config.AttachmentDomain = "foo.bar.com"
	defer func() { rt.Config.AttachmentDomain = "" }()

	tcs := []struct {
		raw        string
		normalized string
	}{
		{"geo:-2.90875,-79.0117686", "geo:-2.90875,-79.0117686"},
		{"image/jpeg:http://files.com/test.jpg", "image/jpeg:http://files.com/test.jpg"},
		{"image/jpeg:https://files.com/test.jpg", "image/jpeg:https://files.com/test.jpg"},
		{"image/jpeg:test.jpg", "image/jpeg:https://foo.bar.com/test.jpg"},
		{"image/jpeg:/test.jpg", "image/jpeg:https://foo.bar.com/test.jpg"},
	}

	for _, tc := range tcs {
		assert.Equal(t, tc.normalized, string(models.NormalizeAttachment(rt.Config, utils.Attachment(tc.raw))))
	}
}

func TestMarkMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "Hello", nil, models.MsgStatusQueued, false)
	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []flows.EventUUID{out1.UUID})
	require.NoError(t, err)
	msg1 := msgs[0]

	out2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "Hola", nil, models.MsgStatusQueued, false)
	msgs, err = models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []flows.EventUUID{out2.UUID})
	require.NoError(t, err)
	msg2 := msgs[0]

	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "Howdy", nil, models.MsgStatusQueued, false)

	models.MarkMessagesForRequeuing(ctx, rt.DB, []*models.Msg{msg1, msg2})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'I'`).Returns(2)

	// try running on database with BIGINT message ids
	rt.DB.MustExec(`ALTER SEQUENCE "msgs_msg_id_seq" AS bigint;`)
	rt.DB.MustExec(`ALTER SEQUENCE "msgs_msg_id_seq" RESTART WITH 3000000000;`)

	out4 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "Big messages!", nil, models.MsgStatusQueued, false)
	msgs, err = models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []flows.EventUUID{out4.UUID})
	require.NoError(t, err)
	msg4 := msgs[0]

	assert.Equal(t, models.MsgID(3000000000), msg4.ID())

	err = models.MarkMessagesForRequeuing(ctx, rt.DB, []*models.Msg{msg4})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'I'`).Returns(3)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'Q'`).Returns(1)

	err = models.MarkMessagesQueued(ctx, rt.DB, []*models.Msg{msg4})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'I'`).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'Q'`).Returns(2)
}

func TestNewIVRMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	vonage := oa.ChannelByUUID(testdb.VonageChannel.UUID)
	cl := testdb.InsertCall(t, rt, testdb.Org1, testdb.VonageChannel, testdb.Ann)
	call, err := models.GetCallByUUID(ctx, rt.DB, testdb.Org1.ID, cl.UUID)
	require.NoError(t, err)

	flow := testdb.Favorites.Load(t, rt, oa)

	flowOut := flows.NewIVRMsgOut(testdb.Ann.URN, vonage.Reference(), "Hello", "http://example.com/hi.mp3", "eng-US")
	eventOut := events.NewIVRCreated(flowOut)
	dbOut := models.NewOutgoingIVR(rt.Config, testdb.Org1.ID, call, flow, eventOut)

	assert.Equal(t, eventOut.UUID(), dbOut.UUID())
	assert.Equal(t, models.MsgTypeVoice, dbOut.Type())
	assert.Equal(t, "Hello", dbOut.Text())
	assert.Equal(t, []utils.Attachment{"audio:http://example.com/hi.mp3"}, dbOut.Attachments())
	assert.Equal(t, i18n.Locale("eng-US"), dbOut.Locale())
	assert.Equal(t, testdb.Favorites.ID, dbOut.FlowID())
	assert.WithinDuration(t, time.Now(), dbOut.CreatedOn(), time.Second)
	assert.WithinDuration(t, time.Now(), *dbOut.SentOn(), time.Second)

	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{dbOut})
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT text, status, msg_type, flow_id FROM msgs_msg WHERE uuid = $1`, dbOut.UUID()).
		Columns(map[string]any{"text": "Hello", "status": "W", "msg_type": "V", "flow_id": testdb.Favorites.ID})

	flowIn := flows.NewMsgIn(testdb.Ann.URN, vonage.Reference(), "1", nil, "")
	eventIn := events.NewMsgReceived(flowIn, "")
	dbIn := models.NewIncomingIVR(rt.Config, testdb.Org1.ID, call, flow, eventIn)

	assert.Equal(t, eventIn.UUID(), dbIn.UUID())
	assert.Equal(t, models.MsgTypeVoice, dbIn.Type())
	assert.Equal(t, "1", dbIn.Text())
	assert.Equal(t, testdb.Favorites.ID, dbIn.FlowID())

	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{dbIn})
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT text, status, msg_type, flow_id FROM msgs_msg WHERE uuid = $1`, dbIn.UUID()).
		Columns(map[string]any{"text": "1", "status": "H", "msg_type": "V", "flow_id": testdb.Favorites.ID})
}

func TestCreateMsgOut(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	// give Ann and Cat new facebook URNs
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Ann, "facebook:123456789", 1001, nil)
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Cat, "facebook:234567890", 1001, nil)

	_, ann, _ := testdb.Ann.Load(t, rt, oa)
	_, bob, _ := testdb.Bob.Load(t, rt, oa)
	_, cat, _ := testdb.Cat.Load(t, rt, oa)
	evalContext := func(c *flows.Contact) *types.XObject {
		return types.NewXObject(map[string]types.XValue{
			"contact": types.NewXObject(map[string]types.XValue{"name": types.NewXText(c.Name())}),
		})
	}

	out, err := models.CreateMsgOut(ctx, rt, oa, bob, &flows.MsgContent{Text: "hello @contact.name"}, models.NilTemplateID, nil, `eng`, evalContext(bob))
	assert.NoError(t, err)
	assert.Equal(t, "hello Bob", out.Text())
	assert.Equal(t, urns.URN("tel:+16055742222"), out.URN())
	assert.Equal(t, assets.NewChannelReference("74729f45-7f29-4868-9dc4-90e491e3c7d8", "Twilio"), out.Channel())
	assert.Equal(t, i18n.Locale(`eng`), out.Locale())
	assert.Nil(t, out.Templating())

	msgContent := &flows.MsgContent{Text: "hello"}
	templateVariables := []string{"@contact.name", "mice"}

	out, err = models.CreateMsgOut(ctx, rt, oa, ann, msgContent, testdb.ReviveTemplate.ID, templateVariables, `eng`, evalContext(ann))
	assert.NoError(t, err)
	assert.Equal(t, "Hi Ann, are you still experiencing problems with mice?", out.Text())
	assert.Equal(t, urns.URN("facebook:123456789"), out.URN())
	assert.Equal(t, assets.NewChannelReference("0f661e8b-ea9d-4bd3-9953-d368340acf91", "Facebook"), out.Channel())
	assert.Equal(t, i18n.Locale(`eng-US`), out.Locale())
	assert.Equal(t, &flows.MsgTemplating{
		Template: assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		Components: []*flows.TemplatingComponent{
			{Name: "body", Type: "body/text", Variables: map[string]int{"1": 0, "2": 1}},
		},
		Variables: []*flows.TemplatingVariable{{Type: "text", Value: "Ann"}, {Type: "text", Value: "mice"}},
	}, out.Templating())

	out, err = models.CreateMsgOut(ctx, rt, oa, cat, msgContent, testdb.ReviveTemplate.ID, templateVariables, `eng`, evalContext(cat))
	assert.NoError(t, err)
	assert.Equal(t, "Hi Cat, are you still experiencing problems with mice?", out.Text())
	assert.Equal(t, &flows.MsgTemplating{
		Template: assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		Components: []*flows.TemplatingComponent{
			{Name: "body", Type: "body/text", Variables: map[string]int{"1": 0, "2": 1}},
		},
		Variables: []*flows.TemplatingVariable{{Type: "text", Value: "Cat"}, {Type: "text", Value: "mice"}},
	}, out.Templating())

	bob.SetStatus(flows.ContactStatusBlocked)

	out, err = models.CreateMsgOut(ctx, rt, oa, bob, &flows.MsgContent{Text: "hello"}, models.NilTemplateID, nil, `eng-US`, nil)
	assert.NoError(t, err)
	assert.Equal(t, urns.URN("tel:+16055742222"), out.URN())
	assert.Equal(t, assets.NewChannelReference("74729f45-7f29-4868-9dc4-90e491e3c7d8", "Twilio"), out.Channel())
	assert.Equal(t, flows.UnsendableReasonContactBlocked, out.UnsendableReason())

	bob.SetStatus(flows.ContactStatusActive)
	bob.SetURNs(nil)

	out, err = models.CreateMsgOut(ctx, rt, oa, bob, &flows.MsgContent{Text: "hello"}, models.NilTemplateID, nil, `eng-US`, nil)
	assert.NoError(t, err)
	assert.Equal(t, urns.NilURN, out.URN())
	assert.Nil(t, out.Channel())
	assert.Equal(t, flows.UnsendableReasonNoRoute, out.UnsendableReason())
}

func TestMsgTemplating(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa := testdb.Org1.Load(t, rt)
	mc, _, _ := testdb.Ann.Load(t, rt, oa)
	channel := oa.ChannelByUUID(testdb.FacebookChannel.UUID)
	chRef := assets.NewChannelReference(testdb.FacebookChannel.UUID, "FB")
	flow, _ := oa.FlowByID(testdb.Favorites.ID)

	templating1 := flows.NewMsgTemplating(
		assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		[]*flows.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
		[]*flows.TemplatingVariable{{Type: "text", Value: "name"}},
	)

	// create a message with templating
	out1 := events.NewMsgCreated(flows.NewMsgOut(testdb.Ann.URN, chRef, &flows.MsgContent{Text: "Hello"}, templating1, i18n.NilLocale, ""), "", "")
	msg1, err := models.NewOutgoingFlowMsg(rt, oa.Org(), channel, mc, flow, out1, nil)
	require.NoError(t, err)

	// create a message without templating
	out2 := events.NewMsgCreated(flows.NewMsgOut(testdb.Ann.URN, chRef, &flows.MsgContent{Text: "Hello"}, nil, i18n.NilLocale, ""), "", "")
	msg2, err := models.NewOutgoingFlowMsg(rt, oa.Org(), channel, mc, flow, out2, nil)
	require.NoError(t, err)

	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{msg1.Msg, msg2.Msg})
	require.NoError(t, err)

	// check non-nil and nil templating writes to db correctly
	assertdb.Query(t, rt.DB, `SELECT templating -> 'template' ->> 'name' FROM msgs_msg WHERE id = $1`, msg1.ID()).Returns("revive_issue")
	assertdb.Query(t, rt.DB, `SELECT templating FROM msgs_msg WHERE id = $1`, msg2.ID()).Returns(nil)

	type testStruct struct {
		Templating *models.Templating `json:"templating"`
	}

	// check non-nil and nil reads from db correctly
	s := &testStruct{}
	err = rt.DB.Get(s, `SELECT templating FROM msgs_msg WHERE id = $1`, msg1.ID())
	assert.NoError(t, err)
	assert.Equal(t, &models.Templating{MsgTemplating: templating1}, s.Templating)

	s = &testStruct{}
	err = rt.DB.Get(s, `SELECT templating FROM msgs_msg WHERE id = $1`, msg2.ID())
	assert.NoError(t, err)
	assert.Nil(t, s.Templating)
}
