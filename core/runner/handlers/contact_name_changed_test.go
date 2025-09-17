package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactNameChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), "Fred"),
					actions.NewSetContactName(flows.NewActionUUID(), "Tarzan"),
				},
				testdb.Cat.UUID: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), "Geoff Newman"),
				},
				testdb.Bob.UUID: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), ""),
				},
				testdb.Dan.UUID: []flows.Action{
					actions.NewSetContactName(flows.NewActionUUID(), "😃234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890"),
				},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   "select count(*) from contacts_contact where name = 'Tarzan' and id = $1",
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
				{
					Query:   "select count(*) from contacts_contact where name = 'Tarzan'",
					Returns: 1,
				},
				{
					Query:   "select count(*) from contacts_contact where name IS NULL and id = $1",
					Args:    []any{testdb.Bob.ID},
					Returns: 1,
				},
				{
					Query:   "select count(*) from contacts_contact where name = 'Geoff Newman' and id = $1",
					Args:    []any{testdb.Cat.ID},
					Returns: 1,
				},
				{
					Query:   "select count(*) from contacts_contact where name = '😃2345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678' and id = $1",
					Args:    []any{testdb.Dan.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_name_changed", "contact_name_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "contact_name_changed", "run_ended"},
				testdb.Cat.UUID: {"run_started", "contact_name_changed", "run_ended"},
				testdb.Dan.UUID: {"run_started", "contact_name_changed", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
