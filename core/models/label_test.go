package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabels(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshLabels)
	require.NoError(t, err)

	labels, err := oa.Labels()
	require.NoError(t, err)

	tcs := []struct {
		ID   models.LabelID
		Name string
	}{
		{testdb.ReportingLabel.ID, "Reporting"},
		{testdb.TestingLabel.ID, "Testing"},
	}

	assert.Equal(t, 3, len(labels))
	for i, tc := range tcs {
		label := labels[i].(*models.Label)
		assert.Equal(t, tc.ID, label.ID())
		assert.Equal(t, tc.Name, label.Name())
		assert.Equal(t, label, oa.LabelByID(tc.ID))
		assert.Equal(t, label, oa.LabelByUUID(label.UUID()))
	}

	assert.Nil(t, oa.LabelByID(models.LabelID(1234)))
}

func TestAddMsgLabels(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	msg1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusHandled, "")
	msg2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusHandled, "")
	msg2.Label(rt, testdb.TestingLabel)

	changed, err := models.AddMsgLabels(ctx, rt.DB, []*models.MsgLabelAdd{
		{MsgUUID: msg1.UUID, LabelID: testdb.ReportingLabel.ID},
		{MsgUUID: msg1.UUID, LabelID: testdb.TestingLabel.ID},
		{MsgUUID: msg2.UUID, LabelID: testdb.TestingLabel.ID}, // already has this label
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []models.MsgID{msg1.ID, msg1.ID}, changed)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, msg1.ID).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, msg2.ID).Returns(1)

	changed, err = models.AddMsgLabels(ctx, rt.DB, nil)
	require.NoError(t, err)
	assert.Empty(t, changed)
}

func TestRemoveMsgLabels(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	msg1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "hi", models.MsgStatusHandled, "")
	msg2 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.TwilioChannel, testdb.Ann, "hello", models.MsgStatusHandled, "")
	msg3 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.TwilioChannel, testdb.Ann, "hey", models.MsgStatusHandled, "")
	msg1.Label(rt, testdb.ReportingLabel, testdb.TestingLabel)
	msg2.Label(rt, testdb.TestingLabel)

	changed, err := models.RemoveMsgLabels(ctx, rt.DB, testdb.TestingLabel.ID, []models.MsgID{msg1.ID, msg3.ID})
	require.NoError(t, err)
	assert.Equal(t, []models.MsgID{msg1.ID}, changed)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, msg1.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg_labels WHERE msg_id = $1`, msg2.ID).Returns(1)

	changed, err = models.RemoveMsgLabels(ctx, rt.DB, testdb.TestingLabel.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, changed)
}
