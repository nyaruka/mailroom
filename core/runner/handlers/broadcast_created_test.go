package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestBroadcastCreated(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Cathy.UUID: []flows.Action{
					actions.NewSendBroadcast(flows.NewActionUUID(), "hello world", nil, nil, nil, nil, "", []urns.URN{urns.URN("tel:+12065551212")}, nil),
				},
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   "select count(*) from flows_flowrun where contact_id = $1 AND status = 'C'",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
			},
			ExpectedTasks: map[string][]string{
				"batch/1": {"send_broadcast"},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Cathy.UUID:     {"run_started", "run_ended"},
				testdb.Bob.UUID:       {"run_started", "run_ended"},
				testdb.George.UUID:    {"run_started", "run_ended"},
				testdb.Alexandra.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
