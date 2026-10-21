package runner_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/utils/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartFlowConcurrency(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// check everything works with big ids
	rt.DB.MustExec(`ALTER SEQUENCE flows_flowrun_id_seq RESTART WITH 5000000000;`)
	rt.DB.MustExec(`ALTER SEQUENCE flows_flowsession_id_seq RESTART WITH 5000000000;`)

	// create a flow which has a send_broadcast action which will mean handlers grabbing redis connections
	flow := testdb.InsertFlow(t, rt, testdb.Org1, testsuite.ReadFile(t, "testdata/broadcast_flow.json"))

	oa := testdb.Org1.Load(t, rt)

	dbFlow, err := oa.FlowByID(flow.ID)
	require.NoError(t, err)
	flowRef := dbFlow.Reference()

	// create a lot of contacts...
	contacts := make([]*testdb.Contact, 100)
	for i := range contacts {
		contacts[i] = testdb.InsertContact(t, rt, testdb.Org1, core.NewContactUUID(), "Jim", i18n.NilLanguage, models.ContactStatusActive)
	}

	triggerBuilder := func() flows.Trigger {
		return triggers.NewBuilder(flowRef).Manual().Build()
	}

	// start each contact in the flow at the same time...
	test.RunConcurrently(len(contacts), func(i int) {
		scenes, _, err := runner.StartWithLock(ctx, rt, oa, []models.ContactID{contacts[i].ID}, triggerBuilder, models.StartModeInterrupt, models.NilStartID, nil)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(scenes))
	})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(len(contacts))
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession`).Returns(len(contacts))
}

func TestLogWithLock(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(t, rt)

	eventBuilder := func() events.Event {
		return events.NewCampaignSkipped(assets.NewCampaignReference(testdb.RemindersCampaign.UUID, "Doctor Reminders"), testdb.RemindersPoint1.UUID, events.CampaignSkippedReasonExistingSession)
	}

	skipped, err := runner.LogWithLock(ctx, rt, oa, []models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, eventBuilder)
	assert.NoError(t, err)
	assert.Len(t, skipped, 0)

	assert.Equal(t, map[core.ContactUUID][]string{
		testdb.Ann.UUID: {"campaign_skipped"},
		testdb.Bob.UUID: {"campaign_skipped"},
	}, testsuite.GetHistoryEventTypes(t, rt, false, time.Time{}))
}
