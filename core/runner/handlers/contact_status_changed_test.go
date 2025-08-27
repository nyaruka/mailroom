package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactStatusChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	tcs := []TestCase{
		{
			Modifiers: ContactModifierMap{
				testdb.Cathy: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusBlocked)},
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND status = 'B'`,
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Cathy.UUID: {"contact_groups_changed"},
			},
		},
		{
			Modifiers: ContactModifierMap{
				testdb.Cathy: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusStopped)},
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND status = 'S'`,
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{},
		},
		{
			Modifiers: ContactModifierMap{
				testdb.Cathy: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusActive)},
			},
			SQLAssertions: []SQLAssertion{
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
			PersistedEvents: map[flows.ContactUUID][]string{},
		},
	}

	runTestCases(t, ctx, rt, tcs)
}
