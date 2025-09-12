package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestSessionTriggered(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	groupRef := &assets.GroupReference{UUID: testdb.TestersGroup.UUID}

	reset := test.MockUniverse()
	defer reset()

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewStartSession(flows.NewActionUUID(), testdb.SingleMessage.Reference(), []*assets.GroupReference{groupRef}, []*flows.ContactReference{testdb.George.Reference()}, "", nil, nil, true),
				},
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   "select count(*) from flows_flowrun where contact_id = $1 AND status = 'C'",
					Args:  []any{testdb.Ann.ID},
					Count: 1,
				},
				{ // start is non-persistent
					SQL:   "select count(*) from flows_flowstart",
					Count: 0,
				},
			},
			ExpectedTasks: map[string][]string{
				"batch/1": {"start_flow"},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID:       {"run_started", "run_ended"},
				testdb.Bob.UUID:       {"run_started", "run_ended"},
				testdb.George.UUID:    {"run_started", "run_ended"},
				testdb.Alexandra.UUID: {"run_started", "run_ended"},
			},
		},
		{
			Actions: ContactActionMap{
				testdb.Bob.UUID: []flows.Action{
					actions.NewStartSession(flows.NewActionUUID(), testdb.IVRFlow.Reference(), nil, []*flows.ContactReference{testdb.Alexandra.Reference()}, "", nil, nil, true),
				},
			},
			SQLAssertions: []SQLAssertion{
				{
					// start is non-persistent
					SQL:   "select count(*) from flows_flowstart",
					Count: 0,
				},
			},
			ExpectedTasks: map[string][]string{
				"batch/1": {"start_flow"},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID:       {"run_started", "run_ended"},
				testdb.Bob.UUID:       {"run_started", "run_ended"},
				testdb.George.UUID:    {"run_started", "run_ended"},
				testdb.Alexandra.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo|testsuite.ResetValkey)
}

func TestQuerySessionTriggered(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	favoriteFlow, err := oa.FlowByID(testdb.Favorites.ID)
	assert.NoError(t, err)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewStartSession(flows.NewActionUUID(), favoriteFlow.Reference(), nil, nil, "name ~ @contact.name", nil, nil, true),
				},
			},
			ExpectedTasks: map[string][]string{
				"batch/1": {"start_flow"},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID:       {"run_started", "run_ended"},
				testdb.Bob.UUID:       {"run_started", "run_ended"},
				testdb.George.UUID:    {"run_started", "run_ended"},
				testdb.Alexandra.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo|testsuite.ResetValkey)
}
