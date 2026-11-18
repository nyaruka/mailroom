package models_test

import (
	"database/sql"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestCalls(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer rt.DB.MustExec(`DELETE FROM ivr_call`)

	oa := testdb.Org1.Load(t, rt)
	ann, _, annURNs := testdb.Ann.Load(t, rt, oa)
	cat, _, catURNs := testdb.Cat.Load(t, rt, oa)

	callIn1 := models.NewIncomingCall(testdb.Org1.ID, oa.ChannelByUUID(testdb.TwilioChannel.UUID), ann, annURNs[0].ID, "EXT123")
	callIn2 := models.NewIncomingCall(testdb.Org1.ID, oa.ChannelByUUID(testdb.VonageChannel.UUID), cat, catURNs[0].ID, "EXT234")

	err := models.InsertCalls(ctx, rt.DB, []*models.Call{callIn1, callIn2})
	assert.NoError(t, err)

	assert.NotEqual(t, models.NilCallID, callIn1.ID())
	assert.NotEqual(t, models.NilCallID, callIn2.ID())

	assertdb.Query(t, rt.DB, `SELECT direction, status, external_id from ivr_call where id = $1`, callIn1.ID()).Columns(map[string]any{"direction": "I", "status": "I", "external_id": "EXT123"})

	trigger := triggers.NewBuilder(testdb.Favorites.Reference()).Manual().Build()
	callOut := models.NewOutgoingCall(testdb.Org1.ID, oa.ChannelByUUID(testdb.TwilioChannel.UUID), ann, annURNs[0].ID, trigger)
	err = models.InsertCalls(ctx, rt.DB, []*models.Call{callOut})
	assert.NoError(t, err)

	assert.NotEqual(t, models.NilCallID, callOut.ID())

	assertdb.Query(t, rt.DB, `SELECT direction, status from ivr_call where id = $1`, callOut.ID()).Columns(map[string]any{"direction": "O", "status": "P"})

	err = callOut.UpdateExternalID(ctx, rt.DB, "EXT345")
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT external_id, status from ivr_call where id = $1`, callOut.ID()).Columns(map[string]any{"external_id": "EXT345", "status": "W"})

	call, err := models.GetCallByUUID(ctx, rt.DB, testdb.Org1.ID, callIn1.UUID())
	assert.NoError(t, err)
	assert.Equal(t, "EXT123", call.ExternalID())

	call, err = models.GetCallByUUID(ctx, rt.DB, testdb.Org1.ID, callOut.UUID())
	assert.NoError(t, err)
	assert.Equal(t, "EXT345", call.ExternalID())

	call, err = models.GetCallByExternalID(ctx, rt.DB, testdb.TwilioChannel.ID, "EXT345")
	assert.NoError(t, err)
	assert.Equal(t, "EXT345", call.ExternalID())
}

func TestGetActiveOutgoingCallByURN(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(t, rt)
	ann, _, annURNs := testdb.Ann.Load(t, rt, oa)
	twilio := oa.ChannelByUUID(testdb.TwilioChannel.UUID)

	// an incoming call from the URN isn't a match
	callIn := models.NewIncomingCall(testdb.Org1.ID, twilio, ann, annURNs[0].ID, "EXT123")
	err := models.InsertCalls(ctx, rt.DB, []*models.Call{callIn})
	assert.NoError(t, err)

	_, err = models.GetActiveOutgoingCallByURN(ctx, rt.DB, testdb.TwilioChannel.ID, testdb.Ann.URN)
	assert.ErrorIs(t, err, sql.ErrNoRows)

	// nor is an outgoing call that hasn't been requested yet
	trigger := triggers.NewBuilder(testdb.Favorites.Reference()).Manual().Build()
	callOut1 := models.NewOutgoingCall(testdb.Org1.ID, twilio, ann, annURNs[0].ID, trigger)
	callOut2 := models.NewOutgoingCall(testdb.Org1.ID, twilio, ann, annURNs[0].ID, trigger)
	err = models.InsertCalls(ctx, rt.DB, []*models.Call{callOut1, callOut2})
	assert.NoError(t, err)

	_, err = models.GetActiveOutgoingCallByURN(ctx, rt.DB, testdb.TwilioChannel.ID, testdb.Ann.URN)
	assert.ErrorIs(t, err, sql.ErrNoRows)

	// once wired it is, and the newest one wins
	assert.NoError(t, callOut1.UpdateExternalID(ctx, rt.DB, "EXT234"))

	call, err := models.GetActiveOutgoingCallByURN(ctx, rt.DB, testdb.TwilioChannel.ID, testdb.Ann.URN)
	assert.NoError(t, err)
	assert.Equal(t, callOut1.UUID(), call.UUID())

	assert.NoError(t, callOut2.UpdateExternalID(ctx, rt.DB, "EXT345"))

	call, err = models.GetActiveOutgoingCallByURN(ctx, rt.DB, testdb.TwilioChannel.ID, testdb.Ann.URN)
	assert.NoError(t, err)
	assert.Equal(t, callOut2.UUID(), call.UUID())

	// but only on the same channel and for the same URN
	_, err = models.GetActiveOutgoingCallByURN(ctx, rt.DB, testdb.VonageChannel.ID, testdb.Ann.URN)
	assert.ErrorIs(t, err, sql.ErrNoRows)

	_, err = models.GetActiveOutgoingCallByURN(ctx, rt.DB, testdb.TwilioChannel.ID, testdb.Bob.URN)
	assert.ErrorIs(t, err, sql.ErrNoRows)

	// and not once ended
	assert.NoError(t, callOut2.SetFailed(ctx, rt.DB))

	call, err = models.GetActiveOutgoingCallByURN(ctx, rt.DB, testdb.TwilioChannel.ID, testdb.Ann.URN)
	assert.NoError(t, err)
	assert.Equal(t, callOut1.UUID(), call.UUID())
}
