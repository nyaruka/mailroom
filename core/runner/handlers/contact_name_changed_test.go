package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactNameChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), "Fred"),
					actions.NewSetContactName(flows.NewActionUUID(), "Tarzan"),
				},
				testdb.George: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), "Geoff Newman"),
				},
				testdb.Bob: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), ""),
				},
				testdb.Alexandra: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), "😃234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890"),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from contacts_contact where name = 'Tarzan' and id = $1",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where name = 'Tarzan'",
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where name IS NULL and id = $1",
					Args:  []any{testdb.Bob.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where name = 'Geoff Newman' and id = $1",
					Args:  []any{testdb.George.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where name = '😃2345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678' and id = $1",
					Args:  []any{testdb.Alexandra.ID},
					Count: 1,
				},
			},
			PersistedEvents: map[string]int{"contact_name_changed": 5, "run_started": 4, "run_ended": 4},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
