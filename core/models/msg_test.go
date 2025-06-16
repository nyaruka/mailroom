package models_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutgoingFlowMsg(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	blake := testdb.InsertContact(rt, testdb.Org1, "79b94a23-6d13-43f4-95fe-c733ee457857", "Blake", i18n.NilLanguage, models.ContactStatusBlocked)
	blakeURNID := testdb.InsertContactURN(rt, testdb.Org1, blake, "tel:++250700000007", 1, nil)

	tcs := []struct {
		Channel      *testdb.Channel
		Contact      *testdb.Contact
		URN          urns.URN
		URNID        models.URNID
		Content      *flows.MsgContent
		Templating   *flows.MsgTemplating
		Locale       i18n.Locale
		Unsendable   flows.UnsendableReason
		Flow         *testdb.Flow
		ResponseTo   *models.MsgInRef
		SuspendedOrg bool

		ExpectedStatus       models.MsgStatus
		ExpectedFailedReason models.MsgFailedReason
		ExpectedMsgCount     int
		ExpectedPriority     bool
	}{
		{ // 0: missing URN ID
			Channel:              testdb.TwilioChannel,
			Contact:              testdb.Cathy,
			URN:                  urns.URN("tel:+250700000001"),
			URNID:                models.URNID(0),
			Content:              &flows.MsgContent{Text: "hello"},
			Flow:                 testdb.Favorites,
			ResponseTo:           &models.MsgInRef{ID: 123425},
			ExpectedStatus:       models.MsgStatusQueued,
			ExpectedFailedReason: models.NilMsgFailedReason,
			ExpectedMsgCount:     1,
			ExpectedPriority:     true,
		},
		{ // 1
			Channel: testdb.TwilioChannel,
			Contact: testdb.Cathy,
			URN:     urns.URN(fmt.Sprintf("tel:+250700000001?id=%d", testdb.Cathy.URNID)),
			URNID:   testdb.Cathy.URNID,
			Content: &flows.MsgContent{
				Text: "test outgoing",
				QuickReplies: []flows.QuickReply{
					{Text: "yes", Extra: "if you want"},
					{Text: "no"},
				},
			},
			Templating: flows.NewMsgTemplating(
				assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
				[]*flows.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
				[]*flows.TemplatingVariable{{Type: "text", Value: "name"}},
			),
			Locale:               "eng-US",
			Flow:                 testdb.SingleMessage,
			ExpectedStatus:       models.MsgStatusQueued,
			ExpectedFailedReason: models.NilMsgFailedReason,
			ExpectedMsgCount:     1,
			ExpectedPriority:     false,
		},
		{ // 2
			Channel:              testdb.TwilioChannel,
			Contact:              testdb.Cathy,
			URN:                  urns.URN(fmt.Sprintf("tel:+250700000001?id=%d", testdb.Cathy.URNID)),
			URNID:                testdb.Cathy.URNID,
			Content:              &flows.MsgContent{Text: "test outgoing", Attachments: []utils.Attachment{utils.Attachment("image/jpeg:https://dl-foo.com/image.jpg")}},
			Flow:                 testdb.Favorites,
			ExpectedStatus:       models.MsgStatusQueued,
			ExpectedFailedReason: models.NilMsgFailedReason,
			ExpectedMsgCount:     2,
			ExpectedPriority:     false,
		},
		{ // 3: suspended org
			Channel:              testdb.TwilioChannel,
			Contact:              testdb.Cathy,
			URN:                  urns.URN(fmt.Sprintf("tel:+250700000001?id=%d", testdb.Cathy.URNID)),
			URNID:                testdb.Cathy.URNID,
			Content:              &flows.MsgContent{Text: "hello"},
			Flow:                 testdb.Favorites,
			SuspendedOrg:         true,
			ExpectedStatus:       models.MsgStatusFailed,
			ExpectedFailedReason: models.MsgFailedSuspended,
			ExpectedMsgCount:     1,
			ExpectedPriority:     false,
		},
		{ // 4: no destination
			Channel:              nil,
			Contact:              testdb.Cathy,
			URN:                  urns.NilURN,
			URNID:                models.URNID(0),
			Content:              &flows.MsgContent{Text: "hello"},
			Unsendable:           flows.UnsendableReasonNoDestination,
			Flow:                 testdb.Favorites,
			ExpectedStatus:       models.MsgStatusFailed,
			ExpectedFailedReason: models.MsgFailedNoDestination,
			ExpectedMsgCount:     1,
			ExpectedPriority:     false,
		},
		{ // 5: blocked contact
			Channel:              testdb.TwilioChannel,
			Contact:              blake,
			URN:                  urns.URN(fmt.Sprintf("tel:+250700000007?id=%d", blakeURNID)),
			URNID:                blakeURNID,
			Content:              &flows.MsgContent{Text: "hello"},
			Unsendable:           flows.UnsendableReasonContactStatus,
			Flow:                 testdb.Favorites,
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

		_, fc, _ := tc.Contact.Load(rt, oa)
		flowMsg := flows.NewMsgOut(tc.URN, chRef, tc.Content, tc.Templating, tc.Locale, tc.Unsendable)
		msg, err := models.NewOutgoingFlowMsg(rt, oa.Org(), ch, fc, flow, flowMsg, tc.ResponseTo, dates.Now())

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
		assert.Equal(t, tc.URNID, msg.ContactURNID(), "%d: urn id mismatch", i)
		assert.Equal(t, tc.Flow.ID, msg.FlowID(), "%d: flow id mismatch", i)

		assert.Equal(t, tc.ExpectedStatus, msg.Status(), "%d: status mismatch", i)
		assert.Equal(t, tc.ExpectedFailedReason, msg.FailedReason(), "%d: failed reason mismatch", i)
		assert.Equal(t, tc.ExpectedMsgCount, msg.MsgCount(), "%d: msg count mismatch", i)
		assert.True(t, msg.ID() > 0)
		assert.True(t, msg.CreatedOn().After(now))
		assert.True(t, msg.ModifiedOn().After(now))
	}

	// check nil failed reasons are saved as NULLs
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE failed_reason IS NOT NULL`).Returns(3)

	// ensure org is unsuspended
	rt.DB.MustExec(`UPDATE orgs_org SET is_suspended = FALSE`)
	models.FlushCache()

	// check encoding of quick replies
	assertdb.Query(t, rt.DB, `SELECT quick_replies[1] FROM msgs_msg WHERE id = 2`).Returns("yes\nif you want")
	assertdb.Query(t, rt.DB, `SELECT quick_replies[2] FROM msgs_msg WHERE id = 2`).Returns("no")

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOrg)
	require.NoError(t, err)
	channel := oa.ChannelByUUID(testdb.TwilioChannel.UUID)
	flow, _ := oa.FlowByID(testdb.Favorites.ID)
	_, fc, _ := testdb.Cathy.Load(rt, oa)

	// check that msg loop detection triggers after 20 repeats of the same text
	newOutgoing := func(text string) *models.MsgOut {
		content := &flows.MsgContent{Text: text}
		flowMsg := flows.NewMsgOut(urns.URN(fmt.Sprintf("tel:+250700000001?id=%d", testdb.Cathy.URNID)), assets.NewChannelReference(testdb.TwilioChannel.UUID, "Twilio"), content, nil, i18n.NilLocale, flows.NilUnsendableReason)
		msg, err := models.NewOutgoingFlowMsg(rt, oa.Org(), channel, fc, flow, flowMsg, nil, dates.Now())
		require.NoError(t, err)
		return msg
	}

	for range 19 {
		msg := newOutgoing("foo")
		assert.Equal(t, models.MsgStatusQueued, msg.Status())
		assert.Equal(t, models.NilMsgFailedReason, msg.FailedReason())
	}
	for range 10 {
		msg := newOutgoing("foo")
		assert.Equal(t, models.MsgStatusFailed, msg.Status())
		assert.Equal(t, models.MsgFailedLooping, msg.FailedReason())
	}
	for range 5 {
		msg := newOutgoing("bar")
		assert.Equal(t, models.MsgStatusQueued, msg.Status())
		assert.Equal(t, models.NilMsgFailedReason, msg.FailedReason())
	}
}

func TestGetMessagesByID(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	msgIn1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "in 1", models.MsgStatusHandled)
	msgOut1 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "out 1", []utils.Attachment{"image/jpeg:hi.jpg"}, models.MsgStatusSent, false)
	msgOut2 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "out 2", nil, models.MsgStatusSent, false)
	msgOut3 := testdb.InsertOutgoingMsg(rt, testdb.Org2, testdb.Org2Channel, testdb.Org2Contact, "out 3", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi 3", nil, models.MsgStatusSent, false)

	ids := []models.MsgID{msgIn1.ID, msgOut1.ID, msgOut2.ID, msgOut3.ID}

	msgs, err := models.GetMessagesByID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, ids)

	// should only return the outgoing messages for this org
	require.NoError(t, err)
	assert.Equal(t, 2, len(msgs))
	assert.Equal(t, "out 1", msgs[0].Text())
	assert.Equal(t, []utils.Attachment{"image/jpeg:hi.jpg"}, msgs[0].Attachments())
	assert.Equal(t, "out 2", msgs[1].Text())

	msgs, err = models.GetMessagesByID(ctx, rt.DB, testdb.Org1.ID, models.DirectionIn, ids)

	// should only return the incoming message for this org
	require.NoError(t, err)
	assert.Equal(t, 1, len(msgs))
	assert.Equal(t, "in 1", msgs[0].Text())
}

func TestResendMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	out1 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", nil, models.MsgStatusFailed, false)
	out2 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Bob, "hi", nil, models.MsgStatusFailed, false)

	// failed message with no channel
	out3 := testdb.InsertOutgoingMsg(rt, testdb.Org1, nil, testdb.Cathy, "hi", nil, models.MsgStatusFailed, false)

	// failed message with no URN
	out4 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL, failed_reason = 'D' WHERE id = $1`, out4.ID)

	// failed message with URN which we no longer have a channel for
	out5 := testdb.InsertOutgoingMsg(rt, testdb.Org1, nil, testdb.George, "hi", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET failed_reason = 'E' WHERE id = $1`, out5.ID)
	rt.DB.MustExec(`UPDATE contacts_contacturn SET scheme = 'viber', path = '1234', identity = 'viber:1234' WHERE id = $1`, testdb.George.URNID)

	// other failed message not included in set to resend
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", nil, models.MsgStatusFailed, false)

	// give Bob's URN an affinity for the Vonage channel
	rt.DB.MustExec(`UPDATE contacts_contacturn SET channel_id = $1 WHERE id = $2`, testdb.VonageChannel.ID, testdb.Bob.URNID)

	ids := []models.MsgID{out1.ID, out2.ID, out3.ID, out4.ID, out5.ID}
	msgs, err := models.GetMessagesByID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, ids)
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
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", nil, models.MsgStatusPending, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Bob, "hi", nil, models.MsgStatusErrored, false)
	out3 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", nil, models.MsgStatusFailed, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", nil, models.MsgStatusQueued, false)
	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.George, "hi", nil, models.MsgStatusQueued, false)

	now := dates.Now()

	// fail the msgs
	err := models.FailChannelMessages(ctx, rt.DB.DB, testdb.Org1.ID, testdb.TwilioChannel.ID, models.MsgFailedChannelRemoved)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' AND modified_on > $1`, now).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' AND failed_reason = 'R' AND modified_on > $1`, now).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT status, failed_reason FROM msgs_msg WHERE id = $1`, out3.ID).Columns(map[string]any{"status": "F", "failed_reason": nil})
}

func TestUpdateMessageDeletedBySender(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	in1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", models.MsgStatusHandled)
	in1.Label(rt, testdb.ReportingLabel, testdb.TestingLabel)
	in2 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "bye", models.MsgStatusHandled)
	in2.Label(rt, testdb.ReportingLabel, testdb.TestingLabel)
	out1 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "hi", nil, models.MsgStatusSent, false)

	err := models.UpdateMessageDeletedBySender(ctx, rt.DB.DB, testdb.Org1.ID, in1.ID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, text FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "X", "text": ""})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, in1.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, in2.ID).Returns(2) // unchanged

	// trying to delete an outgoing message is a noop
	err = models.UpdateMessageDeletedBySender(ctx, rt.DB.DB, testdb.Org1.ID, out1.ID)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, text FROM msgs_msg WHERE id = $1`, out1.ID).Columns(map[string]any{"visibility": "V", "text": "hi"})
}

func TestGetMsgRepetitions(t *testing.T) {
	_, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetValkey)
	defer dates.SetNowFunc(time.Now)

	dates.SetNowFunc(dates.NewFixedNow(time.Date(2021, 11, 18, 12, 13, 3, 234567, time.UTC)))

	oa := testdb.Org1.Load(rt)
	_, cathy, _ := testdb.Cathy.Load(rt, oa)
	_, george, _ := testdb.George.Load(rt, oa)

	msg1 := flows.NewMsgOut(testdb.Cathy.URN, nil, &flows.MsgContent{Text: "foo"}, nil, i18n.NilLocale, flows.NilUnsendableReason)
	msg2 := flows.NewMsgOut(testdb.Cathy.URN, nil, &flows.MsgContent{Text: "FOO"}, nil, i18n.NilLocale, flows.NilUnsendableReason)
	msg3 := flows.NewMsgOut(testdb.Cathy.URN, nil, &flows.MsgContent{Text: "bar"}, nil, i18n.NilLocale, flows.NilUnsendableReason)
	msg4 := flows.NewMsgOut(testdb.George.URN, nil, &flows.MsgContent{Text: "foo"}, nil, i18n.NilLocale, flows.NilUnsendableReason)

	assertRepetitions := func(contact *flows.Contact, m *flows.MsgOut, expected int) {
		count, err := models.GetMsgRepetitions(rt.RP, contact, m)
		require.NoError(t, err)
		assert.Equal(t, expected, count)
	}

	for i := 0; i < 20; i++ {
		assertRepetitions(cathy, msg1, i+1)
	}
	for i := 0; i < 10; i++ {
		assertRepetitions(cathy, msg2, i+21)
	}
	for i := 0; i < 5; i++ {
		assertRepetitions(cathy, msg3, i+1)
	}
	for i := 0; i < 5; i++ {
		assertRepetitions(george, msg4, i+1)
	}
	assertvk.HGetAll(t, rc, "msg_repetitions:2021-11-18T12:15", map[string]string{"10000|foo": "30", "10000|bar": "5", "10002|foo": "5"})
}

func TestNormalizeAttachment(t *testing.T) {
	_, rt := testsuite.Runtime()

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
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	out1 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "Hello", nil, models.MsgStatusQueued, false)
	msgs, err := models.GetMessagesByID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []models.MsgID{out1.ID})
	require.NoError(t, err)
	msg1 := msgs[0]

	out2 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "Hola", nil, models.MsgStatusQueued, false)
	msgs, err = models.GetMessagesByID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []models.MsgID{out2.ID})
	require.NoError(t, err)
	msg2 := msgs[0]

	testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "Howdy", nil, models.MsgStatusQueued, false)

	models.MarkMessagesForRequeuing(ctx, rt.DB, []*models.Msg{msg1, msg2})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'I'`).Returns(2)

	// try running on database with BIGINT message ids
	rt.DB.MustExec(`ALTER SEQUENCE "msgs_msg_id_seq" AS bigint;`)
	rt.DB.MustExec(`ALTER SEQUENCE "msgs_msg_id_seq" RESTART WITH 3000000000;`)

	out4 := testdb.InsertOutgoingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "Big messages!", nil, models.MsgStatusQueued, false)
	msgs, err = models.GetMessagesByID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []models.MsgID{out4.ID})
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

func TestNewOutgoingIVR(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	vonage := oa.ChannelByUUID(testdb.VonageChannel.UUID)
	callID := testdb.InsertCall(rt, testdb.Org1, testdb.VonageChannel, testdb.Cathy)
	call, err := models.GetCallByID(ctx, rt.DB, testdb.Org1.ID, callID)
	require.NoError(t, err)

	flowMsg := flows.NewIVRMsgOut(testdb.Cathy.URN, vonage.Reference(), "Hello", "http://example.com/hi.mp3", "eng-US")
	dbMsg := models.NewOutgoingIVR(rt.Config, testdb.Org1.ID, call, flowMsg, dates.Now())

	assert.Equal(t, flowMsg.UUID(), dbMsg.UUID())
	assert.Equal(t, models.MsgTypeVoice, dbMsg.Type())
	assert.Equal(t, "Hello", dbMsg.Text())
	assert.Equal(t, []utils.Attachment{"audio:http://example.com/hi.mp3"}, dbMsg.Attachments())
	assert.Equal(t, i18n.Locale("eng-US"), dbMsg.Locale())
	assert.WithinDuration(t, time.Now(), dbMsg.CreatedOn(), time.Second)
	assert.WithinDuration(t, time.Now(), *dbMsg.SentOn(), time.Second)

	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{dbMsg})
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT text, status, msg_type FROM msgs_msg WHERE uuid = $1`, dbMsg.UUID()).Columns(map[string]any{"text": "Hello", "status": "W", "msg_type": "V"})
}

func TestCreateMsgOut(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	// give Cathy and George new facebook URNs
	testdb.InsertContactURN(rt, testdb.Org1, testdb.Cathy, "facebook:123456789", 1001, nil)
	testdb.InsertContactURN(rt, testdb.Org1, testdb.George, "facebook:234567890", 1001, nil)

	_, bob, _ := testdb.Bob.Load(rt, oa)
	_, cathy, _ := testdb.Cathy.Load(rt, oa)
	_, george, _ := testdb.George.Load(rt, oa)
	evalContext := func(c *flows.Contact) *types.XObject {
		return types.NewXObject(map[string]types.XValue{
			"contact": types.NewXObject(map[string]types.XValue{"name": types.NewXText(c.Name())}),
		})
	}

	out, ch := models.CreateMsgOut(rt, oa, bob, &flows.MsgContent{Text: "hello @contact.name"}, models.NilTemplateID, nil, `eng`, evalContext(bob))
	assert.Equal(t, "hello Bob", out.Text())
	assert.Equal(t, urns.URN("tel:+16055742222?id=10001"), out.URN())
	assert.Equal(t, assets.NewChannelReference("74729f45-7f29-4868-9dc4-90e491e3c7d8", "Twilio"), out.Channel())
	assert.Equal(t, i18n.Locale(`eng`), out.Locale())
	assert.Nil(t, out.Templating())
	assert.Equal(t, "Twilio", ch.Name())

	msgContent := &flows.MsgContent{Text: "hello"}
	templateVariables := []string{"@contact.name", "mice"}

	out, ch = models.CreateMsgOut(rt, oa, cathy, msgContent, testdb.ReviveTemplate.ID, templateVariables, `eng`, evalContext(cathy))
	assert.Equal(t, "Hi Cathy, are you still experiencing problems with mice?", out.Text())
	assert.Equal(t, urns.URN("facebook:123456789?id=30000"), out.URN())
	assert.Equal(t, assets.NewChannelReference("0f661e8b-ea9d-4bd3-9953-d368340acf91", "Facebook"), out.Channel())
	assert.Equal(t, i18n.Locale(`eng-US`), out.Locale())
	assert.Equal(t, &flows.MsgTemplating{
		Template: assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		Components: []*flows.TemplatingComponent{
			{Name: "body", Type: "body/text", Variables: map[string]int{"1": 0, "2": 1}},
		},
		Variables: []*flows.TemplatingVariable{{Type: "text", Value: "Cathy"}, {Type: "text", Value: "mice"}},
	}, out.Templating())
	assert.Equal(t, "Facebook", ch.Name())

	out, _ = models.CreateMsgOut(rt, oa, george, msgContent, testdb.ReviveTemplate.ID, templateVariables, `eng`, evalContext(george))
	assert.Equal(t, "Hi George, are you still experiencing problems with mice?", out.Text())
	assert.Equal(t, &flows.MsgTemplating{
		Template: assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		Components: []*flows.TemplatingComponent{
			{Name: "body", Type: "body/text", Variables: map[string]int{"1": 0, "2": 1}},
		},
		Variables: []*flows.TemplatingVariable{{Type: "text", Value: "George"}, {Type: "text", Value: "mice"}},
	}, out.Templating())

	bob.SetStatus(flows.ContactStatusBlocked)

	out, ch = models.CreateMsgOut(rt, oa, bob, &flows.MsgContent{Text: "hello"}, models.NilTemplateID, nil, `eng-US`, nil)
	assert.Equal(t, urns.URN("tel:+16055742222?id=10001"), out.URN())
	assert.Equal(t, assets.NewChannelReference("74729f45-7f29-4868-9dc4-90e491e3c7d8", "Twilio"), out.Channel())
	assert.Equal(t, "Twilio", ch.Name())
	assert.Equal(t, flows.UnsendableReasonContactStatus, out.UnsendableReason())

	bob.SetStatus(flows.ContactStatusActive)
	bob.ClearURNs()

	out, ch = models.CreateMsgOut(rt, oa, bob, &flows.MsgContent{Text: "hello"}, models.NilTemplateID, nil, `eng-US`, nil)
	assert.Equal(t, urns.NilURN, out.URN())
	assert.Nil(t, out.Channel())
	assert.Nil(t, ch)
	assert.Equal(t, flows.UnsendableReasonNoDestination, out.UnsendableReason())
}

func TestMsgTemplating(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa := testdb.Org1.Load(rt)
	_, fc, _ := testdb.Cathy.Load(rt, oa)
	channel := oa.ChannelByUUID(testdb.FacebookChannel.UUID)
	chRef := assets.NewChannelReference(testdb.FacebookChannel.UUID, "FB")
	flow, _ := oa.FlowByID(testdb.Favorites.ID)

	templating1 := flows.NewMsgTemplating(
		assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		[]*flows.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
		[]*flows.TemplatingVariable{{Type: "text", Value: "name"}},
	)

	// create a message with templating
	out1 := flows.NewMsgOut(testdb.Cathy.URN, chRef, &flows.MsgContent{Text: "Hello"}, templating1, i18n.NilLocale, flows.NilUnsendableReason)
	msg1, err := models.NewOutgoingFlowMsg(rt, oa.Org(), channel, fc, flow, out1, nil, dates.Now())
	require.NoError(t, err)

	// create a message without templating
	out2 := flows.NewMsgOut(testdb.Cathy.URN, chRef, &flows.MsgContent{Text: "Hello"}, nil, i18n.NilLocale, flows.NilUnsendableReason)
	msg2, err := models.NewOutgoingFlowMsg(rt, oa.Org(), channel, fc, flow, out2, nil, dates.Now())
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
