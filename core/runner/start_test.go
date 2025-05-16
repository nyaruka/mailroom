package runner_test

import (
	"testing"

	"github.com/lib/pq"
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

func TestStartFlowBatch(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll) // because it changes contacts

	oa := testdata.Org1.Load(rt)

	// create a start object
	start1 := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdata.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdata.Cathy.ID, testdata.Bob.ID, testdata.George.ID, testdata.Alexandria.ID})
	err := models.InsertFlowStarts(ctx, rt.DB, []*models.FlowStart{start1})
	require.NoError(t, err)

	batch1 := start1.CreateBatch([]models.ContactID{testdata.Cathy.ID, testdata.Bob.ID}, true, false, 4)

	// start the first batch...
	sessions, err := runner.StartFlowBatch(ctx, rt, oa, start1, batch1)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = ANY($1) 
		AND status = 'C' AND call_id IS NULL AND output IS NOT NULL`, pq.Array([]models.ContactID{testdata.Cathy.ID, testdata.Bob.ID})).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE contact_id = ANY($1) and flow_id = $2 AND responded = FALSE AND org_id = 1 AND status = 'C'
		AND results IS NOT NULL AND path_nodes IS NOT NULL AND session_uuid IS NOT NULL`, pq.Array([]models.ContactID{testdata.Cathy.ID, testdata.Bob.ID}), testdata.SingleMessage.ID).
		Returns(2)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = ANY($1) AND text = 'Hey, how are you?' AND org_id = 1 AND status = 'Q' 
		AND direction = 'O' AND msg_type = 'T'`, pq.Array([]models.ContactID{testdata.Cathy.ID, testdata.Bob.ID})).
		Returns(2)

	// create a start object with params
	testdata.InsertFlowStart(rt, testdata.Org1, testdata.Admin, testdata.IncomingExtraFlow, nil)
	start2 := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdata.IncomingExtraFlow.ID).
		WithContactIDs([]models.ContactID{testdata.Cathy.ID}).
		WithParams([]byte(`{"name":"Fred", "age":33}`))
	err = models.InsertFlowStarts(ctx, rt.DB, []*models.FlowStart{start2})
	require.NoError(t, err)

	batch2 := start2.CreateBatch([]models.ContactID{testdata.Cathy.ID}, false, true, 1)

	sessions, err = runner.StartFlowBatch(ctx, rt, oa, start2, batch2)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text = 'Great to meet you Fred. Your age is 33.'`).Returns(1)
}

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
	flowRef := testdata.Favorites.Reference()

	// create a lot of contacts...
	contacts := make([]*testdata.Contact, 100)
	for i := range contacts {
		contacts[i] = testdata.InsertContact(rt, testdata.Org1, flows.NewContactUUID(), "Jim", i18n.NilLanguage, models.ContactStatusActive)
	}

	options := &runner.StartOptions{
		TriggerBuilder: func(contact *flows.Contact) flows.Trigger {
			return triggers.NewBuilder(oa.Env(), flowRef, contact).Manual().Build()
		},
	}

	// start each contact in the flow at the same time...
	test.RunConcurrently(len(contacts), func(i int) {
		sessions, err := runner.StartFlowWithLock(ctx, rt, oa, dbFlow, []models.ContactID{contacts[i].ID}, options, models.NilStartID, nil)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(sessions))
	})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(len(contacts))
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession`).Returns(len(contacts))
}
