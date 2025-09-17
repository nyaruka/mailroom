package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestRunStarted(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	oa := testdb.Org1.Load(rt)

	flow, err := oa.FlowByID(testdb.PickANumber.ID)
	assert.NoError(t, err)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewEnterFlow(flows.NewActionUUID(), flow.Reference(), false),
				},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE current_flow_id = $1`,
					Args:    []any{flow.ID()},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "run_started"},
				testdb.Bob.UUID: {"run_started", "run_ended"},
				testdb.Cat.UUID: {"run_started", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
