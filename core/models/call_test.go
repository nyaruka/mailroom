package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestCalls(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer rt.DB.MustExec(`DELETE FROM ivr_call`)

	oa := testdata.Org1.Load(rt)
	cathy, _, cathyURNs := testdata.Cathy.Load(rt, oa)
	george, _, georgeURNs := testdata.George.Load(rt, oa)

	callIn1 := models.NewIncomingCall(testdata.Org1.ID, oa.ChannelByUUID(testdata.TwilioChannel.UUID), cathy, cathyURNs[0].ID, "EXT123")
	callIn2 := models.NewIncomingCall(testdata.Org1.ID, oa.ChannelByUUID(testdata.VonageChannel.UUID), george, georgeURNs[0].ID, "EXT234")

	err := models.InsertCalls(ctx, rt.DB, []*models.Call{callIn1, callIn2})
	assert.NoError(t, err)

	assert.NotEqual(t, models.NilCallID, callIn1.ID())
	assert.NotEqual(t, models.NilCallID, callIn2.ID())

	assertdb.Query(t, rt.DB, `SELECT status, external_id from ivr_call where id = $1`, callIn1.ID()).Columns(map[string]any{"status": "I", "external_id": "EXT123"})

	call, err := models.InsertCall(ctx, rt.DB, testdata.Org1.ID, testdata.TwilioChannel.ID, models.NilStartID, testdata.Cathy.ID, testdata.Cathy.URNID, models.DirectionOut, models.CallStatusPending, "")
	assert.NoError(t, err)

	assert.NotEqual(t, models.CallID(0), call.ID())

	err = call.UpdateExternalID(ctx, rt.DB, "test1")
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) from ivr_call where external_id = 'test1' AND id = $1`, call.ID()).Returns(1)

	conn2, err := models.GetCallByID(ctx, rt.DB, testdata.Org1.ID, call.ID())
	assert.NoError(t, err)
	assert.Equal(t, "test1", conn2.ExternalID())
}
