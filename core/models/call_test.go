package models_test

import (
	"fmt"
	"testing"
	"time"

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

func TestCallTallies(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	defer rt.DB.MustExec(`DELETE FROM ivr_call`)

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

	models.IncrementCallTallies(ctx, rt, calls)

	// tallies are by the UTC day the call was created on
	day := time.Now().UTC().Format(time.DateOnly)

	assertvk.HGetAll(t, vc, "{tallies}:live", map[string]string{
		fmt.Sprintf("C%d|calls:in@%s", testdb.TwilioChannel.ID, day):  "1",
		fmt.Sprintf("C%d|calls:out@%s", testdb.TwilioChannel.ID, day): "1",
		fmt.Sprintf("C%d|calls:in@%s", testdb.VonageChannel.ID, day):  "1",
		"O1|calls/in@" + day:  "2",
		"O1|calls/out@" + day: "1",
	})
}
