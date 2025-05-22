package runner_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/nyaruka/mailroom/utils/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartFlowConcurrency(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	// check everything works with big ids
	rt.DB.MustExec(`ALTER SEQUENCE flows_flowrun_id_seq RESTART WITH 5000000000;`)
	rt.DB.MustExec(`ALTER SEQUENCE flows_flowsession_id_seq RESTART WITH 5000000000;`)

	// create a flow which has a send_broadcast action which will mean handlers grabbing redis connections
	flow := testdata.InsertFlow(rt, testdata.Org1, testsuite.ReadFile("testdata/broadcast_flow.json"))

	oa := testdata.Org1.Load(rt)

	dbFlow, err := oa.FlowByID(flow.ID)
	require.NoError(t, err)
	flowRef := dbFlow.Reference()

	// create a lot of contacts...
	contacts := make([]*testdata.Contact, 100)
	for i := range contacts {
		contacts[i] = testdata.InsertContact(rt, testdata.Org1, flows.NewContactUUID(), "Jim", i18n.NilLanguage, models.ContactStatusActive)
	}

	triggerBuilder := func(contact *flows.Contact) flows.Trigger {
		return triggers.NewBuilder(oa.Env(), flowRef, contact).Manual().Build()
	}

	// start each contact in the flow at the same time...
	test.RunConcurrently(len(contacts), func(i int) {
		sessions, err := runner.StartWithLock(ctx, rt, oa, []models.ContactID{contacts[i].ID}, triggerBuilder, false, models.NilStartID, nil)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(sessions))
	})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(len(contacts))
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession`).Returns(len(contacts))
}
