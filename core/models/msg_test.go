package models_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutgoingFlowMsg(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	blake := testdb.InsertContact(t, rt, testdb.Org1, "79b94a23-6d13-43f4-95fe-c733ee457857", "Blake", i18n.NilLanguage, models.ContactStatusBlocked)
	blakeURNID := testdb.InsertContactURN(t, rt, testdb.Org1, blake, "tel:+250700000007", 1, nil)

	tcs := []struct {
		Channel      *testdb.Channel
		Contact      *testdb.Contact
		URN          urns.URN
		Content      *core.MsgContent
		Templating   *core.MsgTemplating
		Locale       i18n.Locale
		Unsendable   core.UnsendableReason
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
			Content: &core.MsgContent{
				Text: "test outgoing",
				QuickReplies: []core.QuickReply{
					{Type: "text", Text: "yes", Extra: "if you want"},
					{Type: "text", Text: "no"},
				},
			},
			Templating: core.NewMsgTemplating(
				assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
				[]*core.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
				[]*core.TemplatingVariable{{Type: "text", Value: "name"}},
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
			Content:              &core.MsgContent{Text: "test outgoing", Attachments: []utils.Attachment{utils.Attachment("image/jpeg:https://dl-foo.com/image.jpg")}},
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
			Content:              &core.MsgContent{Text: "hello"},
			Unsendable:           core.UnsendableReasonNoRoute,
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
			Content:              &core.MsgContent{Text: "hello"},
			Unsendable:           core.UnsendableReasonContactBlocked,
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
		rt.DB.MustExec(`UPDATE orgs_org SET is_suspended = $1, suspended_on = CASE WHEN $1 THEN NOW() END WHERE id = $2`, tc.SuspendedOrg, testdb.Org1.ID)

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
		msgEvent := events.NewMsgCreated(core.NewMsgOut(tc.URN, chRef, tc.Content, tc.Templating, tc.Locale, tc.Unsendable), "", "")
		msg, err := models.NewOutgoingFlowMsg(rt, oa.Org(), ch, mc, flow, msgEvent, tc.ResponseTo)
		assert.NoError(t, err)

		expectedAttachments := tc.Content.Attachments
		if expectedAttachments == nil {
			expectedAttachments = []utils.Attachment{}
		}
		expectedQuickReplies := tc.Content.QuickReplies
		if expectedQuickReplies == nil {
			expectedQuickReplies = []core.QuickReply{}
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

	// every message we insert gets a folder - queued messages go to the outbox, unsendable ones straight to failed
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE folder IS NULL`).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'Q' AND folder != 'O'`).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' AND folder != 'X'`).Returns(0)

	// check writing of quick replies
	assertdb.Query(t, rt.DB, `SELECT quickreplies::text FROM msgs_msg WHERE id = 30000`).Returns(`[{"text": "yes", "type": "text", "extra": "if you want"}, {"text": "no", "type": "text"}]`)
	assertdb.Query(t, rt.DB, `SELECT quickreplies FROM msgs_msg WHERE id = 30001`).Returns(nil)
}

func TestDeriveMsgFolder(t *testing.T) {
	const in, out = models.DirectionIn, models.DirectionOut
	const visible, archived = models.VisibilityVisible, models.VisibilityArchived
	const delUser, delSender = models.VisibilityDeletedByUser, models.VisibilityDeletedBySender

	tcs := []struct {
		direction  models.Direction
		status     models.MsgStatus
		visibility models.MsgVisibility
		hasFlow    bool
		expected   models.MsgFolder
	}{
		{in, models.MsgStatusHandled, visible, false, models.MsgFolderInbox},
		{in, models.MsgStatusHandled, visible, true, models.MsgFolderHandled},
		{in, models.MsgStatusHandled, archived, false, models.MsgFolderArchived},
		{in, models.MsgStatusHandled, archived, true, models.MsgFolderArchived}, // flow is irrelevant once archived
		{in, models.MsgStatusPending, visible, false, models.MsgFolderPending},
		{out, models.MsgStatusInitializing, visible, false, models.MsgFolderOutbox},
		{out, models.MsgStatusQueued, visible, false, models.MsgFolderOutbox},
		{out, models.MsgStatusErrored, visible, false, models.MsgFolderOutbox},
		{out, models.MsgStatusWired, visible, false, models.MsgFolderSent},
		{out, models.MsgStatusSent, visible, false, models.MsgFolderSent},
		{out, models.MsgStatusDelivered, visible, false, models.MsgFolderSent},
		{out, models.MsgStatusRead, visible, false, models.MsgFolderSent},
		{out, models.MsgStatusFailed, visible, false, models.MsgFolderFailed},

		// deleted takes precedence over everything else
		{in, models.MsgStatusHandled, delUser, false, models.MsgFolderDeleted},
		{in, models.MsgStatusHandled, delSender, true, models.MsgFolderDeleted},
		{in, models.MsgStatusPending, delUser, false, models.MsgFolderDeleted},   // deleted whilst still pending
		{in, models.MsgStatusPending, delSender, false, models.MsgFolderDeleted}, // deleted whilst still pending
		{out, models.MsgStatusSent, delUser, false, models.MsgFolderDeleted},

		// pending takes precedence over the user facing folders, so an archived message which hasn't been handled
		// yet is pending rather than archived
		{in, models.MsgStatusPending, archived, false, models.MsgFolderPending},
	}

	for i, tc := range tcs {
		actual := models.DeriveMsgFolder(tc.direction, tc.status, tc.visibility, tc.hasFlow)
		assert.Equal(t, tc.expected, actual, "%d: folder mismatch for %s/%s/%s", i, tc.direction, tc.status, tc.visibility)
	}

	// states which shouldn't exist panic rather than leaving a message in no folder
	assert.Panics(t, func() { models.DeriveMsgFolder(out, models.MsgStatusHandled, visible, false) })
	assert.Panics(t, func() { models.DeriveMsgFolder(out, models.MsgStatusPending, visible, false) }) // incoming only status
	assert.Panics(t, func() { models.DeriveMsgFolder(out, models.MsgStatusSent, archived, false) })
	assert.Panics(t, func() { models.DeriveMsgFolder(in, models.MsgStatusErrored, visible, false) })
}

func TestGetMessagesByUUID(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	msgIn1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-d4be-76c7-8a5c-a12caae7aa87", testdb.TwilioChannel, testdb.Ann, "in 1", models.MsgStatusHandled, "")
	msgOut1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "out 1", []utils.Attachment{"image/jpeg:hi.jpg"}, models.MsgStatusSent, false)
	msgOut2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "out 2", nil, models.MsgStatusSent, false)
	msgOut3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org2, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.Org2Channel, testdb.Org2Contact, "out 3", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "hi 3", nil, models.MsgStatusSent, false)

	uuids := []events.EventUUID{msgIn1.UUID, msgOut1.UUID, msgOut2.UUID, msgOut3.UUID}

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

func TestMarkMessageHandled(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	flow, err := oa.FlowByID(testdb.Favorites.ID)
	require.NoError(t, err)

	in1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusPending, "")
	in2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusPending, "")
	in3 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusPending, "")

	// a message deleted by the user whilst it was still waiting to be handled, and one deleted by its sender
	in4 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusPending, "")
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D', text = '' WHERE id = $1`, in4.ID)
	in5 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bb0a-4c2e-7a51-8f3d-1c6b5e9d0a72", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusPending, "")
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'X', folder = 'D', text = '' WHERE id = $1`, in5.ID)

	// a message handled outside of a flow ends up in the inbox
	err = models.MarkMessageHandled(ctx, rt.DB, in1.UUID, models.MsgStatusHandled, models.VisibilityVisible, nil, nil, nil, nil)
	assert.NoError(t, err)

	// one handled by a flow ends up in the handled folder
	err = models.MarkMessageHandled(ctx, rt.DB, in2.UUID, models.MsgStatusHandled, models.VisibilityVisible, flow, nil, nil, nil)
	assert.NoError(t, err)

	// and one from a blocked contact or an inactive channel is archived
	err = models.MarkMessageHandled(ctx, rt.DB, in3.UUID, models.MsgStatusHandled, models.VisibilityArchived, nil, nil, nil, nil)
	assert.NoError(t, err)

	// handling a message deleted whilst it was pending doesn't resurrect it, however it was deleted
	err = models.MarkMessageHandled(ctx, rt.DB, in4.UUID, models.MsgStatusHandled, models.VisibilityVisible, nil, nil, nil, nil)
	assert.NoError(t, err)
	err = models.MarkMessageHandled(ctx, rt.DB, in5.UUID, models.MsgStatusHandled, models.VisibilityVisible, nil, nil, nil, nil)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status, visibility, folder, flow_id FROM msgs_msg WHERE id = $1`, in1.ID).
		Columns(map[string]any{"status": "H", "visibility": "V", "folder": "I", "flow_id": nil})
	assertdb.Query(t, rt.DB, `SELECT status, visibility, folder, flow_id FROM msgs_msg WHERE id = $1`, in2.ID).
		Columns(map[string]any{"status": "H", "visibility": "V", "folder": "W", "flow_id": testdb.Favorites.ID})
	assertdb.Query(t, rt.DB, `SELECT status, visibility, folder, flow_id FROM msgs_msg WHERE id = $1`, in3.ID).
		Columns(map[string]any{"status": "H", "visibility": "A", "folder": "A", "flow_id": nil})
	assertdb.Query(t, rt.DB, `SELECT status, visibility, folder, text FROM msgs_msg WHERE id = $1`, in4.ID).
		Columns(map[string]any{"status": "P", "visibility": "D", "folder": "D", "text": ""})
	assertdb.Query(t, rt.DB, `SELECT status, visibility, folder, text FROM msgs_msg WHERE id = $1`, in5.ID).
		Columns(map[string]any{"status": "P", "visibility": "X", "folder": "D", "text": ""})
}

func TestResendMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

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

	// failed message which has since been deleted
	out6 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb97-6d69-7e33-9f9e-1bd9dbd9f68e", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D', text = '' WHERE id = $1`, out6.ID)

	// other failed message not included in set to resend
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb98-3637-778d-9dfc-0ab85c950d7c", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)

	// give Bob's URN an affinity for the Vonage channel
	rt.DB.MustExec(`UPDATE contacts_contacturn SET channel_id = $1 WHERE id = $2`, testdb.VonageChannel.ID, testdb.Bob.URNID)

	uuids := []events.EventUUID{out1.UUID, out2.UUID, out3.UUID, out4.UUID, out5.UUID, out6.UUID}
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

	// the returned messages should report the status they were actually written with
	for i, m := range resent {
		assert.Equal(t, models.MsgStatusQueued, m.Status(), "%d: status mismatch", i)
	}

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'Q' AND folder = 'O' AND sent_on IS NULL`).Returns(3)

	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out4.ID).Columns(map[string]any{"status": "F", "folder": "X", "failed_reason": "D"})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out5.ID).Columns(map[string]any{"status": "F", "folder": "X", "failed_reason": "D"})

	// the deleted message is left in the deleted folder rather than being resurrected
	assertdb.Query(t, rt.DB, `SELECT status, folder, visibility FROM msgs_msg WHERE id = $1`, out6.ID).Columns(map[string]any{"status": "F", "folder": "D", "visibility": "D"})
}

func TestFailMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Bob, "hi", nil, models.MsgStatusErrored, false)
	out3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusFailed, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb96-3c4c-72f2-bacc-4b6ae4c592b3", testdb.TwilioChannel, testdb.Cat, "hi", nil, models.MsgStatusQueued, false)

	// a message which never made it into courier's queue
	out6 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb97-6d69-7e33-9f9e-1bd9dbd9f68e", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusInitializing, false)

	// and one which has since been deleted
	out7 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb98-3637-778d-9dfc-0ab85c950d7c", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D', text = '' WHERE id = $1`, out7.ID)

	now := dates.Now()

	// fail the msgs
	err := models.FailChannelMessages(ctx, rt.DB.DB, testdb.Org1.ID, testdb.TwilioChannel.ID, models.MsgFailedChannelRemoved)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' AND modified_on > $1`, now).Returns(5)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'F' AND failed_reason = 'R' AND modified_on > $1`, now).Returns(5)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE folder = 'X'`).Returns(6) // including the already failed one
	assertdb.Query(t, rt.DB, `SELECT status, failed_reason FROM msgs_msg WHERE id = $1`, out3.ID).Columns(map[string]any{"status": "F", "failed_reason": nil})

	// the message that never reached courier is failed too
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out6.ID).Columns(map[string]any{"status": "F", "folder": "X"})

	// but the deleted one is left alone
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out7.ID).Columns(map[string]any{"status": "Q", "folder": "D"})
}

func TestFailOldAndroidMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	fortnightAgo := time.Now().Add(-14 * 24 * time.Hour)
	yesterday := time.Now().Add(-24 * time.Hour)

	// stale android messages still waiting to be sent
	out1 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hi", models.MsgStatusInitializing, fortnightAgo)
	out2 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.AndroidChannel, testdb.Bob, "hi", models.MsgStatusQueued, fortnightAgo)
	out3 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.AndroidChannel, testdb.Cat, "hi", models.MsgStatusErrored, fortnightAgo)

	// an equally old android message that already reached the channel, and one that already failed
	out4 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.AndroidChannel, testdb.Ann, "hi", models.MsgStatusWired, fortnightAgo)
	out5 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb96-3c4c-72f2-bacc-4b6ae4c592b3", testdb.AndroidChannel, testdb.Ann, "hi", models.MsgStatusFailed, fortnightAgo)

	// a stale queued message on a non-android channel - still in courier's queue so not ours to fail
	out6 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb97-6d69-7e33-9f9e-1bd9dbd9f68e", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusQueued, fortnightAgo)

	// a stale android message in another workspace
	out7 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org2, "0199bb98-98e1-7b6c-b8a7-8b2f5d0f7e9c", testdb.Org2Channel, testdb.Org2Contact, "hi", models.MsgStatusQueued, fortnightAgo)
	rt.DB.MustExec(`UPDATE msgs_msg SET is_android = TRUE WHERE id = $1`, out7.ID) // org 2 has no android channel

	// and one that's only a day old so its relayer may yet sync
	out8 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb99-d64d-7bb1-9a1e-9d3c4f6ae3c1", testdb.AndroidChannel, testdb.Ann, "hi", models.MsgStatusQueued, yesterday)

	// a stale android message which has since been deleted
	out9 := testdb.InsertOutgoingMsgCreatedOn(t, rt, testdb.Org1, "0199bb9a-8a56-7c48-b1e8-6a5f0d1c8b47", testdb.AndroidChannel, testdb.Ann, "hi", models.MsgStatusQueued, fortnightAgo)
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D', text = '' WHERE id = $1`, out9.ID)

	olderThan := time.Now().Add(-7 * 24 * time.Hour)

	// messages are failed in batches of the given size
	tags1, err := models.FailOldAndroidMessages(ctx, rt.DB, olderThan, 2)
	assert.NoError(t, err)
	assert.Len(t, tags1, 2)

	tags2, err := models.FailOldAndroidMessages(ctx, rt.DB, olderThan, 2)
	assert.NoError(t, err)
	assert.Len(t, tags2, 2)

	// and then there's nothing left to fail
	tags3, err := models.FailOldAndroidMessages(ctx, rt.DB, olderThan, 2)
	assert.NoError(t, err)
	assert.Len(t, tags3, 0)

	// each failure is tagged against the message's own event, for the message's contact
	byMsg := make(map[events.EventUUID]*models.EventTag, 4)
	for _, tag := range append(tags1, tags2...) {
		byMsg[tag.EventUUID] = tag
	}
	assert.ElementsMatch(t, []events.EventUUID{out1.UUID, out2.UUID, out3.UUID, out7.UUID}, slices.Collect(maps.Keys(byMsg)))

	if assert.Contains(t, byMsg, events.EventUUID(out7.UUID)) {
		tag := byMsg[out7.UUID]
		assert.Equal(t, testdb.Org2.ID, tag.OrgID)
		assert.Equal(t, testdb.Org2Contact.UUID, tag.ContactUUID)
		assert.Equal(t, "sts", tag.Tag)
		assert.Equal(t, "failed", tag.Data["status"])
		assert.Equal(t, "too_old", tag.Data["reason"])
	}

	// the stale outbox messages are now failed and moved to the failed folder
	for _, m := range []*testdb.MsgOut{out1, out2, out3, out7} {
		assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, m.ID).
			Columns(map[string]any{"status": "F", "folder": "X", "failed_reason": "O"})
	}

	// but nothing else was touched
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out4.ID).
		Columns(map[string]any{"status": "W", "folder": "S", "failed_reason": nil})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out5.ID).
		Columns(map[string]any{"status": "F", "folder": "X", "failed_reason": nil})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out6.ID).
		Columns(map[string]any{"status": "Q", "folder": "O", "failed_reason": nil})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out8.ID).
		Columns(map[string]any{"status": "Q", "folder": "O", "failed_reason": nil})
	assertdb.Query(t, rt.DB, `SELECT status, folder, failed_reason FROM msgs_msg WHERE id = $1`, out9.ID).
		Columns(map[string]any{"status": "Q", "folder": "D", "failed_reason": nil})
}

func TestUpdateAndroidMessageStatuses(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	sentOn := time.Date(2025, 5, 4, 12, 30, 45, 0, time.UTC)
	dlvdOn := time.Date(2025, 5, 4, 12, 31, 45, 0, time.UTC)

	// messages which are still waiting to be sent, and so have no sent_on
	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)
	out2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.AndroidChannel, testdb.Bob, "hi", nil, models.MsgStatusQueued, false)
	out3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.AndroidChannel, testdb.Cat, "hi", nil, models.MsgStatusQueued, false)
	out4 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)

	// a message which already has a sent_on, and one which is incoming
	out5 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb09-f0e9-7489-a58e-69304a7941a0", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusSent, false)
	rt.DB.MustExec(`UPDATE msgs_msg SET sent_on = $2 WHERE id = $1`, out5.ID, sentOn)
	in1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.AndroidChannel, testdb.Ann, "hi", models.MsgStatusHandled, "")

	// and one in another workspace
	out6 := testdb.InsertOutgoingMsg(t, rt, testdb.Org2, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.Org2Channel, testdb.Org2Contact, "hi", nil, models.MsgStatusQueued, false)

	tags, err := models.UpdateAndroidMessageStatuses(ctx, rt.DB, testdb.Org1.ID, []*models.AndroidStatusUpdate{
		{MsgUUID: out1.UUID, Status: models.MsgStatusErrored},
		{MsgUUID: out2.UUID, Status: models.MsgStatusFailed},
		{MsgUUID: out3.UUID, Status: models.MsgStatusSent, SentOn: &sentOn, OverwriteSentOn: true},
		{MsgUUID: out4.UUID, Status: models.MsgStatusDelivered, SentOn: &dlvdOn},
		{MsgUUID: out5.UUID, Status: models.MsgStatusDelivered, SentOn: &dlvdOn},
		{MsgUUID: in1.UUID, Status: models.MsgStatusSent, SentOn: &sentOn, OverwriteSentOn: true},
		{MsgUUID: out6.UUID, Status: models.MsgStatusFailed},
		{MsgUUID: "0199bb96-3c4c-72f2-bacc-4b6ae4c592b3", Status: models.MsgStatusFailed},
	})
	assert.NoError(t, err)

	// two updates for the same message is a coding error
	_, err = models.UpdateAndroidMessageStatuses(ctx, rt.DB, testdb.Org1.ID, []*models.AndroidStatusUpdate{
		{MsgUUID: out1.UUID, Status: models.MsgStatusSent, SentOn: &sentOn},
		{MsgUUID: out1.UUID, Status: models.MsgStatusDelivered, SentOn: &dlvdOn},
	})
	assert.EqualError(t, err, "more than one update for message 0199bad8-f98d-75a3-b641-2718a25ac3f5")

	// errored messages stay in the outbox, and only the messages we could update are tagged
	assertdb.Query(t, rt.DB, `SELECT status, folder, sent_on FROM msgs_msg WHERE id = $1`, out1.ID).
		Columns(map[string]any{"status": "E", "folder": "O", "sent_on": nil})
	assertdb.Query(t, rt.DB, `SELECT status, folder, sent_on FROM msgs_msg WHERE id = $1`, out2.ID).
		Columns(map[string]any{"status": "F", "folder": "X", "sent_on": nil})
	assertdb.Query(t, rt.DB, `SELECT status, folder, sent_on FROM msgs_msg WHERE id = $1`, out3.ID).
		Columns(map[string]any{"status": "S", "folder": "S", "sent_on": sentOn})

	// delivery fills in sent_on when there isn't one, but never replaces one
	assertdb.Query(t, rt.DB, `SELECT status, folder, sent_on FROM msgs_msg WHERE id = $1`, out4.ID).
		Columns(map[string]any{"status": "D", "folder": "S", "sent_on": dlvdOn})
	assertdb.Query(t, rt.DB, `SELECT status, folder, sent_on FROM msgs_msg WHERE id = $1`, out5.ID).
		Columns(map[string]any{"status": "D", "folder": "S", "sent_on": sentOn})

	// incoming messages and messages in other workspaces are left alone
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, in1.ID).
		Columns(map[string]any{"status": "H", "folder": "I"})
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out6.ID).
		Columns(map[string]any{"status": "Q", "folder": "O"})

	byMsg := make(map[events.EventUUID]*models.EventTag, len(tags))
	for _, tag := range tags {
		byMsg[tag.EventUUID] = tag
	}
	assert.ElementsMatch(t, []events.EventUUID{out1.UUID, out2.UUID, out3.UUID, out4.UUID, out5.UUID}, slices.Collect(maps.Keys(byMsg)))

	if assert.Contains(t, byMsg, events.EventUUID(out2.UUID)) {
		tag := byMsg[out2.UUID]
		assert.Equal(t, testdb.Org1.ID, tag.OrgID)
		assert.Equal(t, testdb.Bob.UUID, tag.ContactUUID)
		assert.Equal(t, "sts", tag.Tag)
		assert.Equal(t, "failed", tag.Data["status"])
		assert.NotContains(t, tag.Data, "reason")
	}

	// nothing to do is not an error
	tags, err = models.UpdateAndroidMessageStatuses(ctx, rt.DB, testdb.Org1.ID, nil)
	assert.NoError(t, err)
	assert.Len(t, tags, 0)
}

func TestGetAndroidOutbox(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hello", nil, models.MsgStatusQueued, false)
	out2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.AndroidChannel, testdb.Bob, "hello", nil, models.MsgStatusQueued, false)
	out3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.AndroidChannel, testdb.Cat, "goodbye", nil, models.MsgStatusQueued, false)

	// messages that have already been sent or failed aren't offered again, nor are those on other channels
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.AndroidChannel, testdb.Ann, "sent", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb09-f0e9-7489-a58e-69304a7941a0", testdb.AndroidChannel, testdb.Ann, "errored", nil, models.MsgStatusErrored, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "other channel", nil, models.MsgStatusQueued, false)

	msgs, err := models.GetAndroidOutbox(ctx, rt.DB, testdb.AndroidChannel.ID, nil, 100)
	assert.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.Equal(t, out1.ID, msgs[0].ID)
	assert.Equal(t, "hello", msgs[0].Text)
	assert.Equal(t, "+16055741111", msgs[0].Phone)
	assert.Equal(t, out2.ID, msgs[1].ID)
	assert.Equal(t, out3.ID, msgs[2].ID)

	// messages the relayer says it already has are excluded
	msgs, err = models.GetAndroidOutbox(ctx, rt.DB, testdb.AndroidChannel.ID, []models.MsgID{out1.ID, out3.ID}, 100)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, out2.ID, msgs[0].ID)

	// and we never offer more than the caller asked for
	msgs, err = models.GetAndroidOutbox(ctx, rt.DB, testdb.AndroidChannel.ID, nil, 2)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)

	// a message without a URN can't be sent by a relayer so isn't offered at all
	rt.DB.MustExec(`UPDATE msgs_msg SET contact_urn_id = NULL WHERE id = $1`, out1.ID)
	msgs, err = models.GetAndroidOutbox(ctx, rt.DB, testdb.AndroidChannel.ID, nil, 100)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)
}

func TestArchiveAndRestoreMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	in1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusHandled, "")
	in2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "bye", models.MsgStatusHandled, "")
	in3 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "not handled yet", models.MsgStatusPending, "")

	// second message was handled by a flow so it's in the handled folder rather than the inbox
	rt.DB.MustExec(`UPDATE msgs_msg SET flow_id = $1, folder = 'W' WHERE id = $2`, testdb.Favorites.ID, in2.ID)

	load := func(uuids ...events.EventUUID) []*models.Msg {
		msgs, err := models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionIn, uuids)
		require.NoError(t, err)
		return msgs
	}

	err := models.ArchiveMessages(ctx, rt.DB, load(in1.UUID, in2.UUID, in3.UUID))
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "A", "folder": "A"})
	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in2.ID).Columns(map[string]any{"visibility": "A", "folder": "A"})
	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in3.ID).Columns(map[string]any{"visibility": "A", "folder": "P"})

	// archiving again is a noop
	err = models.ArchiveMessages(ctx, rt.DB, load(in1.UUID))
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "A", "folder": "A"})

	// restoring puts each message back in the folder it came from
	err = models.RestoreMessages(ctx, rt.DB, load(in1.UUID, in2.UUID, in3.UUID))
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "V", "folder": "I"})
	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in2.ID).Columns(map[string]any{"visibility": "V", "folder": "W"})
	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in3.ID).Columns(map[string]any{"visibility": "V", "folder": "P"})

	// restoring a message that isn't archived is a noop
	err = models.RestoreMessages(ctx, rt.DB, load(in1.UUID))
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "V", "folder": "I"})

	// a message deleted after being loaded but before being updated stays deleted
	loaded := load(in2.UUID)
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D', text = '' WHERE id = $1`, in2.ID)

	err = models.ArchiveMessages(ctx, rt.DB, loaded)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, folder, text FROM msgs_msg WHERE id = $1`, in2.ID).Columns(map[string]any{"visibility": "D", "folder": "D", "text": ""})

	// deleted messages can't be archived
	rt.DB.MustExec(`UPDATE msgs_msg SET visibility = 'D', folder = 'D' WHERE id = $1`, in1.ID)

	err = models.ArchiveMessages(ctx, rt.DB, load(in1.UUID))
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT visibility, folder FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "D", "folder": "D"})
}

func TestDeleteMessages(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	in1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusHandled, "")
	in1.Label(rt, testdb.ReportingLabel, testdb.TestingLabel)
	in2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "bye", models.MsgStatusHandled, "")
	in2.Label(rt, testdb.ReportingLabel, testdb.TestingLabel)
	in3 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "3", models.MsgStatusHandled, "")
	in4 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.TwilioChannel, testdb.Ann, "4", models.MsgStatusHandled, "")
	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb96-3c4c-72f2-bacc-4b6ae4c592b3", testdb.TwilioChannel, testdb.Ann, "hi", nil, models.MsgStatusSent, false)

	tx := rt.DB.MustBegin()

	err := models.DeleteMessages(ctx, tx, testdb.Org1.ID, []events.EventUUID{in1.UUID}, models.VisibilityDeletedBySender)
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, `SELECT visibility, folder, text FROM msgs_msg WHERE id = $1`, in1.ID).Columns(map[string]any{"visibility": "X", "folder": "D", "text": ""})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, in1.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, in2.ID).Returns(2) // unchanged

	tx = rt.DB.MustBegin()

	err = models.DeleteMessages(ctx, tx, testdb.Org1.ID, []events.EventUUID{in3.UUID, in4.UUID}, models.VisibilityDeletedByUser)
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, `SELECT visibility, folder, text FROM msgs_msg WHERE id = $1`, in3.ID).Columns(map[string]any{"visibility": "D", "folder": "D", "text": ""})
	assertdb.Query(t, rt.DB, `SELECT visibility, folder, text FROM msgs_msg WHERE id = $1`, in4.ID).Columns(map[string]any{"visibility": "D", "folder": "D", "text": ""})

	tx = rt.DB.MustBegin()

	// trying to delete an outgoing message is a noop
	err = models.DeleteMessages(ctx, tx, testdb.Org1.ID, []events.EventUUID{out1.UUID}, models.VisibilityDeletedBySender)
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, `SELECT visibility, folder, text FROM msgs_msg WHERE id = $1`, out1.ID).Columns(map[string]any{"visibility": "V", "folder": "S", "text": "hi"})
}

func TestGetMsgRepetitions(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer dates.SetNowFunc(time.Now)

	dates.SetNowFunc(dates.NewFixedNow(time.Date(2021, 11, 18, 12, 13, 3, 234567, time.UTC)))

	msg1 := &core.MsgContent{Text: "foo"}
	msg2 := &core.MsgContent{Text: "FOO"}
	msg3 := &core.MsgContent{Text: "bar"}
	msg4 := &core.MsgContent{Text: "foo"}

	assertRepetitions := func(contactID models.ContactID, m *core.MsgContent, expected int) {
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

	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "Hello", nil, models.MsgStatusQueued, false)
	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []events.EventUUID{out1.UUID})
	require.NoError(t, err)
	msg1 := msgs[0]

	out2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "Hola", nil, models.MsgStatusQueued, false)
	msgs, err = models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []events.EventUUID{out2.UUID})
	require.NoError(t, err)
	msg2 := msgs[0]

	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb93-ec0f-703e-9b5b-d26d4b6b133c", testdb.TwilioChannel, testdb.Ann, "Howdy", nil, models.MsgStatusQueued, false)

	models.MarkMessagesForRequeuing(ctx, rt.DB, []*models.Msg{msg1, msg2})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE status = 'I'`).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE folder = 'O'`).Returns(3) // all still in outbox

	// try running on database with BIGINT message ids
	rt.DB.MustExec(`ALTER SEQUENCE "msgs_msg_id_seq" AS bigint;`)
	rt.DB.MustExec(`ALTER SEQUENCE "msgs_msg_id_seq" RESTART WITH 3000000000;`)

	out4 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb94-1134-75d6-91dc-8aee7787f703", testdb.TwilioChannel, testdb.Ann, "Big messages!", nil, models.MsgStatusQueued, false)
	msgs, err = models.GetMessagesByUUID(ctx, rt.DB, testdb.Org1.ID, models.DirectionOut, []events.EventUUID{out4.UUID})
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
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE folder = 'O'`).Returns(4)
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

	flowOut := core.NewIVRMsgOut(testdb.Ann.URN, vonage.Reference(), "Hello", "http://example.com/hi.mp3", "eng-US")
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

	assertdb.Query(t, rt.DB, `SELECT text, status, folder, msg_type, flow_id FROM msgs_msg WHERE uuid = $1`, dbOut.UUID()).
		Columns(map[string]any{"text": "Hello", "status": "W", "folder": "S", "msg_type": "V", "flow_id": testdb.Favorites.ID})

	flowIn := core.NewMsgIn(testdb.Ann.URN, vonage.Reference(), "1", nil, "", nil)
	eventIn := events.NewMsgReceived(flowIn, "")
	dbIn := models.NewIncomingIVR(rt.Config, testdb.Org1.ID, call, flow, eventIn)

	assert.Equal(t, eventIn.UUID(), dbIn.UUID())
	assert.Equal(t, models.MsgTypeVoice, dbIn.Type())
	assert.Equal(t, "1", dbIn.Text())
	assert.Equal(t, testdb.Favorites.ID, dbIn.FlowID())

	err = models.InsertMessages(ctx, rt.DB, []*models.Msg{dbIn})
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT text, status, folder, msg_type, flow_id FROM msgs_msg WHERE uuid = $1`, dbIn.UUID()).
		Columns(map[string]any{"text": "1", "status": "H", "folder": "W", "msg_type": "V", "flow_id": testdb.Favorites.ID})
}

func TestCreateMsgOut(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	// give Ann and Cat new facebook URNs
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Ann, "facebook:123456789", 1001, nil)
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Cat, "facebook:234567890", 1001, nil)

	_, ann, _ := testdb.Ann.Load(t, rt, oa)
	_, bob, _ := testdb.Bob.Load(t, rt, oa)
	_, cat, _ := testdb.Cat.Load(t, rt, oa)
	evalContext := func(c *core.Contact) *types.XObject {
		return types.NewXObject(map[string]types.XValue{
			"contact": types.NewXObject(map[string]types.XValue{"name": types.NewXText(c.Name())}),
		})
	}

	out, err := models.CreateMsgOut(ctx, rt, oa, bob, &core.MsgContent{Text: "hello @contact.name"}, models.NilTemplateID, nil, `eng`, evalContext(bob))
	assert.NoError(t, err)
	assert.Equal(t, "hello Bob", out.Text())
	assert.Equal(t, urns.URN("tel:+16055742222"), out.URN())
	assert.Equal(t, assets.NewChannelReference("74729f45-7f29-4868-9dc4-90e491e3c7d8", "Twilio"), out.Channel())
	assert.Equal(t, i18n.Locale(`eng`), out.Locale())
	assert.Nil(t, out.Templating())

	msgContent := &core.MsgContent{Text: "hello"}
	templateVariables := []string{"@contact.name", "mice"}

	out, err = models.CreateMsgOut(ctx, rt, oa, ann, msgContent, testdb.ReviveTemplate.ID, templateVariables, `eng`, evalContext(ann))
	assert.NoError(t, err)
	assert.Equal(t, "Hi Ann, are you still experiencing problems with mice?", out.Text())
	assert.Equal(t, urns.URN("facebook:123456789"), out.URN())
	assert.Equal(t, assets.NewChannelReference("0f661e8b-ea9d-4bd3-9953-d368340acf91", "Facebook"), out.Channel())
	assert.Equal(t, i18n.Locale(`eng-US`), out.Locale())
	assert.Equal(t, &core.MsgTemplating{
		Template: assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		Components: []*core.TemplatingComponent{
			{Name: "body", Type: "body/text", Variables: map[string]int{"1": 0, "2": 1}},
		},
		Variables: []*core.TemplatingVariable{{Type: "text", Value: "Ann"}, {Type: "text", Value: "mice"}},
	}, out.Templating())

	out, err = models.CreateMsgOut(ctx, rt, oa, cat, msgContent, testdb.ReviveTemplate.ID, templateVariables, `eng`, evalContext(cat))
	assert.NoError(t, err)
	assert.Equal(t, "Hi Cat, are you still experiencing problems with mice?", out.Text())
	assert.Equal(t, &core.MsgTemplating{
		Template: assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		Components: []*core.TemplatingComponent{
			{Name: "body", Type: "body/text", Variables: map[string]int{"1": 0, "2": 1}},
		},
		Variables: []*core.TemplatingVariable{{Type: "text", Value: "Cat"}, {Type: "text", Value: "mice"}},
	}, out.Templating())

	bob.SetStatus(core.ContactStatusBlocked)

	out, err = models.CreateMsgOut(ctx, rt, oa, bob, &core.MsgContent{Text: "hello"}, models.NilTemplateID, nil, `eng-US`, nil)
	assert.NoError(t, err)
	assert.Equal(t, urns.URN("tel:+16055742222"), out.URN())
	assert.Equal(t, assets.NewChannelReference("74729f45-7f29-4868-9dc4-90e491e3c7d8", "Twilio"), out.Channel())
	assert.Equal(t, core.UnsendableReasonContactBlocked, out.UnsendableReason())

	bob.SetStatus(core.ContactStatusActive)
	bob.SetRoutes(nil)

	out, err = models.CreateMsgOut(ctx, rt, oa, bob, &core.MsgContent{Text: "hello"}, models.NilTemplateID, nil, `eng-US`, nil)
	assert.NoError(t, err)
	assert.Equal(t, urns.NilURN, out.URN())
	assert.Nil(t, out.Channel())
	assert.Equal(t, core.UnsendableReasonNoRoute, out.UnsendableReason())
}

func TestMsgTemplating(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(t, rt)
	mc, _, _ := testdb.Ann.Load(t, rt, oa)
	channel := oa.ChannelByUUID(testdb.FacebookChannel.UUID)
	chRef := assets.NewChannelReference(testdb.FacebookChannel.UUID, "FB")
	flow, _ := oa.FlowByID(testdb.Favorites.ID)

	templating1 := core.NewMsgTemplating(
		assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue"),
		[]*core.TemplatingComponent{{Type: "body", Name: "body", Variables: map[string]int{"1": 0}}},
		[]*core.TemplatingVariable{{Type: "text", Value: "name"}},
	)

	// create a message with templating
	out1 := events.NewMsgCreated(core.NewMsgOut(testdb.Ann.URN, chRef, &core.MsgContent{Text: "Hello"}, templating1, i18n.NilLocale, ""), "", "")
	msg1, err := models.NewOutgoingFlowMsg(rt, oa.Org(), channel, mc, flow, out1, nil)
	require.NoError(t, err)

	// create a message without templating
	out2 := events.NewMsgCreated(core.NewMsgOut(testdb.Ann.URN, chRef, &core.MsgContent{Text: "Hello"}, nil, i18n.NilLocale, ""), "", "")
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
