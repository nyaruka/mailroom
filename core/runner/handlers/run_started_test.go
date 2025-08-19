package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestFlowEntered(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	oa := testdb.Org1.Load(rt)

	flow, err := oa.FlowByID(testdb.PickANumber.ID)
	assert.NoError(t, err)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewEnterFlow(flows.NewActionUUID(), flow.Reference(), false),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   `SELECT count(*) FROM contacts_contact WHERE current_flow_id = $1`,
					Args:  []any{flow.ID()},
					Count: 1,
				},
			},
			PersistedEvents: map[string]int{},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
