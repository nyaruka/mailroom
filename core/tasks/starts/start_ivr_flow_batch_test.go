package starts_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/starts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/require"
)

func TestIVR(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetAll)

	// register our mock client
	ivr.RegisterService(models.ChannelType("ZZ"), testsuite.NewIVRServiceFactory)

	// update our twilio channel to be of type 'ZZ' and set max_concurrent_events to 1
	rt.DB.MustExec(`UPDATE channels_channel SET channel_type = 'ZZ', config = '{"max_concurrent_calls": 1}' WHERE id = $1`, testdata.TwilioChannel.ID)

	// create a flow start for cathy
	start := models.NewFlowStart(testdata.Org1.ID, models.StartTypeTrigger, testdata.IVRFlow.ID).
		WithContactIDs([]models.ContactID{testdata.Cathy.ID})
	err := models.InsertFlowStarts(ctx, rt.DB, []*models.FlowStart{start})
	require.NoError(t, err)

	testsuite.IVRService.CallError = fmt.Errorf("unable to create call")

	err = tasks.Queue(rc, tasks.BatchQueue, testdata.Org1.ID, &starts.StartFlowTask{FlowStart: start}, false)
	require.NoError(t, err)

	testsuite.FlushTasks(t, rt)

	// should have one call in a failed state
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2`, testdata.Cathy.ID, models.CallStatusFailed).Returns(1)

	// re-queue the start and try again
	testsuite.IVRService.CallError = nil
	testsuite.IVRService.CallID = ivr.CallID("call1")

	err = tasks.Queue(rc, tasks.BatchQueue, testdata.Org1.ID, &starts.StartFlowTask{FlowStart: start}, false)
	require.NoError(t, err)

	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2 AND external_id = $3`, testdata.Cathy.ID, models.CallStatusWired, "call1").Returns(1)

	// trying again should put us in a throttled state (queued)
	testsuite.IVRService.CallError = nil
	testsuite.IVRService.CallID = ivr.CallID("call1")

	err = tasks.Queue(rc, tasks.BatchQueue, testdata.Org1.ID, &starts.StartFlowTask{FlowStart: start}, false)
	require.NoError(t, err)

	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2 AND next_attempt IS NOT NULL;`, testdata.Cathy.ID, models.CallStatusQueued).Returns(1)
}
