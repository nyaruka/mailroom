package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactStatusChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	tcs := []handlers.TestCase{
		{
			Modifiers: handlers.ContactModifierMap{
				testdb.Cathy: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusBlocked)},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND status = 'B'`,
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
			},
		},
		{
			Modifiers: handlers.ContactModifierMap{
				testdb.Cathy: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusStopped)},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND status = 'S'`,
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
			},
		},
		{
			Modifiers: handlers.ContactModifierMap{
				testdb.Cathy: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusActive)},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND status = 'A'`,
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND status = 'A'`,
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
