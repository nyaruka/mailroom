package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactStatusChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetDB)

	tcs := []TestCase{
		{
			Modifiers: ContactModifierMap{
				testdb.Ann.UUID: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusBlocked)},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'B'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"contact_status_changed", "contact_groups_changed"},
			},
		},
		{
			Modifiers: ContactModifierMap{
				testdb.Ann.UUID: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusStopped)},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{testdb.Ann.UUID: {"contact_status_changed"}},
		},
		{
			Modifiers: ContactModifierMap{
				testdb.Ann.UUID: []flows.Modifier{modifiers.NewStatus(flows.ContactStatusActive)},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'A'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
				{
					Query:   `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'A'`,
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{testdb.Ann.UUID: {"contact_status_changed"}},
		},
	}

	runTestCases(t, ctx, rt, tcs)
}
