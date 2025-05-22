package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows/triggers"
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

	assertdb.Query(t, rt.DB, `SELECT direction, status, external_id from ivr_call where id = $1`, callIn1.ID()).Columns(map[string]any{"direction": "I", "status": "I", "external_id": "EXT123"})

	trigger := triggers.NewBuilder(oa.Env(), testdata.Favorites.Reference(), nil).Manual().Build()
	callOut := models.NewOutgoingCall(testdata.Org1.ID, oa.ChannelByUUID(testdata.TwilioChannel.UUID), cathy, cathyURNs[0].ID, trigger)
	err = models.InsertCalls(ctx, rt.DB, []*models.Call{callOut})
	assert.NoError(t, err)

	assert.NotEqual(t, models.NilCallID, callOut.ID())

	assertdb.Query(t, rt.DB, `SELECT direction, status from ivr_call where id = $1`, callOut.ID()).Columns(map[string]any{"direction": "O", "status": "P"})

	err = callOut.UpdateExternalID(ctx, rt.DB, "EXT345")
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT external_id, status from ivr_call where id = $1`, callOut.ID()).Columns(map[string]any{"external_id": "EXT345", "status": "W"})

	call, err := models.GetCallByID(ctx, rt.DB, testdata.Org1.ID, callIn1.ID())
	assert.NoError(t, err)
	assert.Equal(t, "EXT123", call.ExternalID())

	call, err = models.GetCallByID(ctx, rt.DB, testdata.Org1.ID, callOut.ID())
	assert.NoError(t, err)
	assert.Equal(t, "EXT345", call.ExternalID())
}
