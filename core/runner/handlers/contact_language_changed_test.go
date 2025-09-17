package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactLanguageChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewSetContactLanguage(flows.NewActionUUID(), "fra"),
					actions.NewSetContactLanguage(flows.NewActionUUID(), "eng"),
				},
				testdb.Cat.UUID: []flows.Action{
					actions.NewSetContactLanguage(flows.NewActionUUID(), "spa"),
				},
				testdb.Dan.UUID: []flows.Action{
					actions.NewSetContactLanguage(flows.NewActionUUID(), ""),
				},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   "select count(*) from contacts_contact where id = $1 and language = 'eng'",
					Args:    []any{testdb.Ann.ID},
					Returns: 1,
				},
				{
					Query:   "select count(*) from contacts_contact where id = $1 and language = 'spa'",
					Args:    []any{testdb.Cat.ID},
					Returns: 1,
				},
				{
					Query:   "select count(*) from contacts_contact where id = $1 and language is NULL;",
					Args:    []any{testdb.Bob.ID},
					Returns: 1,
				},
				{
					Query:   "select count(*) from contacts_contact where id = $1 and language is NULL;",
					Args:    []any{testdb.Dan.ID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_language_changed", "contact_language_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "run_ended"},
				testdb.Cat.UUID: {"run_started", "contact_language_changed", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
