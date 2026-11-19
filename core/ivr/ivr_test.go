package ivr_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
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

	ivr.RegisterService(models.ChannelType("ZZ"), testsuite.NewIVRServiceFactory)
	rt.DB.MustExec(`UPDATE channels_channel SET channel_type = 'ZZ' WHERE id = $1`, testdb.TwilioChannel.ID)

	testsuite.IVRService.CallError = nil
	testsuite.IVRService.CallID = ivr.CallID("call1")

	requestCall := func() *models.Call {
		oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
		require.NoError(t, err)
		flow, err := oa.FlowByID(testdb.IVRFlow.ID)
		require.NoError(t, err)
		mc, _, _ := testdb.Ann.Load(t, rt, oa)

		call, err := ivr.RequestCall(ctx, rt, oa, mc, triggers.NewBuilder(flow.Reference()).Manual().Build())
		require.NoError(t, err)
		require.NotNil(t, call)
		return call
	}

	call := requestCall()
	assert.Equal(t, models.CallStatusWired, call.Status())
	assertdb.Query(t, rt.DB, `SELECT status, external_id, error_reason FROM ivr_call WHERE id = $1`, call.ID()).
		Columns(map[string]any{"status": "W", "external_id": "call1", "error_reason": nil})

	// a suspended workspace gets a failed call which is never requested from the provider
	testdb.Org1.SetStatus(t, rt, models.OrgStatusSuspended)
	testsuite.IVRService.CallID = ivr.CallID("call2")

	call = requestCall()
	assert.Equal(t, models.CallStatusFailed, call.Status())
	assert.Equal(t, models.CallErrorSuspended, call.ErrorReason())
	assertdb.Query(t, rt.DB, `SELECT status, external_id, error_reason FROM ivr_call WHERE id = $1`, call.ID()).
		Columns(map[string]any{"status": "F", "external_id": "", "error_reason": "S"})
}
