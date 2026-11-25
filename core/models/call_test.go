package models_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCallTallies(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	defer rt.DB.MustExec(`DELETE FROM ivr_call`)
	defer dates.SetNowFunc(time.Now)

	// tallies are by the UTC day the call was created on
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2026, 11, 3, 23, 30, 0, 0, time.FixedZone("", -2*3600))))

	oa := testdb.Org1.Load(t, rt)
	ann, _, annURNs := testdb.Ann.Load(t, rt, oa)
	twilio, vonage := oa.ChannelByUUID(testdb.TwilioChannel.UUID), oa.ChannelByUUID(testdb.VonageChannel.UUID)
	trigger := triggers.NewBuilder(testdb.Favorites.Reference()).Manual().Build()

	calls := []*models.Call{
		models.NewIncomingCall(testdb.Org1.ID, twilio, ann, annURNs[0].ID, "EXT1"),
		models.NewIncomingCall(testdb.Org1.ID, vonage, ann, annURNs[0].ID, "EXT2"),
		models.NewOutgoingCall(testdb.Org1.ID, twilio, ann, annURNs[0].ID, trigger),
	}
	require.NoError(t, models.InsertCalls(ctx, rt.DB, calls))

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM ivr_call WHERE created_on = '2026-11-04T01:30:00Z'`).Returns(3)

	models.IncrementCallTallies(ctx, rt, testdb.Org1.ID, calls)

	assertvk.HGetAll(t, vc, "{tallies}:live", map[string]string{
		fmt.Sprintf("C%d|calls:in@2026-11-04", testdb.TwilioChannel.ID):  "1",
		fmt.Sprintf("C%d|calls:out@2026-11-04", testdb.TwilioChannel.ID): "1",
		fmt.Sprintf("C%d|calls:in@2026-11-04", testdb.VonageChannel.ID):  "1",
		"O1|calls/in@2026-11-04":  "2",
		"O1|calls/out@2026-11-04": "1",
	})

	// minutes are rounded up per call and only what a call's duration has gained since is added
	assert.Nil(t, models.CallMinsTallies(calls[0], 0))
	prev, err := calls[0].UpdateStatusAndDuration(ctx, rt.DB, models.CallStatusCompleted, 61, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, prev)
	assert.Equal(t, map[string]int64{
		fmt.Sprintf("C%d|callmins:in@2026-11-04", testdb.TwilioChannel.ID): 2,
		"O1|calls/mins:in@2026-11-04":                                      2,
	}, models.CallMinsTallies(calls[0], 0))
	assert.Equal(t, map[string]int64{
		fmt.Sprintf("C%d|callmins:in@2026-11-04", testdb.TwilioChannel.ID): 1,
		"O1|calls/mins:in@2026-11-04":                                      1,
	}, models.CallMinsTallies(calls[0], 30))
	assert.Nil(t, models.CallMinsTallies(calls[0], 61))
	assert.Nil(t, models.CallMinsTallies(calls[0], 120))

	// a smaller duration doesn't overwrite a larger one
	prev, err = calls[0].UpdateStatusAndDuration(ctx, rt.DB, models.CallStatusCompleted, 30, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 61, prev)
	assert.Nil(t, models.CallMinsTallies(calls[0], prev))
	assertdb.Query(t, rt.DB, `SELECT duration FROM ivr_call WHERE id = $1`, calls[0].ID()).Returns(61)

	_, err = calls[2].UpdateStatusAndDuration(ctx, rt.DB, models.CallStatusCompleted, 25, time.Now())
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{
		fmt.Sprintf("C%d|callmins:out@2026-11-04", testdb.TwilioChannel.ID): 1,
		"O1|calls/mins:out@2026-11-04":                                      1,
	}, models.CallMinsTallies(calls[2], 0))
}
