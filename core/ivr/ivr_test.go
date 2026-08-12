package ivr_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/v26/core/ivr"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestCall(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// register our mock client
	ivr.RegisterService(models.ChannelType("ZZ"), testsuite.NewIVRServiceFactory)

	// update our twilio channel to be of type 'ZZ'
	rt.DB.MustExec(`UPDATE channels_channel SET channel_type = 'ZZ' WHERE id = $1`, testdb.TwilioChannel.ID)

	testsuite.IVRService.CallError = nil
	testsuite.IVRService.CallID = ivr.CallID("call1")

	oa := testdb.Org1.Load(t, rt)
	contact, _, _ := testdb.Ann.Load(t, rt, oa)
	flow := testdb.IVRFlow.Load(t, rt, oa)
	trigger := triggers.NewBuilder(flow.Reference()).Manual().Build()

	call, err := ivr.RequestCall(ctx, rt, oa, contact, trigger)
	require.NoError(t, err)
	assert.Equal(t, models.CallStatusWired, call.Status())
	assert.Equal(t, "call1", call.ExternalID())

	// suspend the org and request another call
	rt.DB.MustExec(`UPDATE orgs_org SET is_suspended = TRUE WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()
	oa = testdb.Org1.Load(t, rt)

	call, err = ivr.RequestCall(ctx, rt, oa, contact, trigger)
	require.NoError(t, err)

	// call should be failed without ever being requested from the provider
	assert.Equal(t, models.CallStatusFailed, call.Status())
	assert.Equal(t, "", call.ExternalID())
}
