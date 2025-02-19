package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestFlowEntered(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	oa := testdata.Org1.Load(rt)

	flow, err := oa.FlowByID(testdata.PickANumber.ID)
	assert.NoError(t, err)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdata.Cathy: []flows.Action{
					actions.NewEnterFlow(handlers.NewActionUUID(), flow.Reference(), false),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   `SELECT count(*) FROM contacts_contact WHERE current_flow_id = $1`,
					Args:  []any{flow.ID()},
					Count: 1,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
