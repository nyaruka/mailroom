package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactLanguageChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewSetContactLanguage(flows.NewActionUUID(), "fra"),
					actions.NewSetContactLanguage(flows.NewActionUUID(), "eng"),
				},
				testdb.George: []flows.Action{
					actions.NewSetContactLanguage(flows.NewActionUUID(), "spa"),
				},
				testdb.Alexandra: []flows.Action{
					actions.NewSetContactLanguage(flows.NewActionUUID(), ""),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language = 'eng'",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language = 'spa'",
					Args:  []any{testdb.George.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language is NULL;",
					Args:  []any{testdb.Bob.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language is NULL;",
					Args:  []any{testdb.Alexandra.ID},
					Count: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Cathy.UUID:     {"run_started", "contact_language_changed", "contact_language_changed", "run_ended"},
				testdb.Bob.UUID:       {"run_started", "run_ended"},
				testdb.George.UUID:    {"run_started", "contact_language_changed", "run_ended"},
				testdb.Alexandra.UUID: {"run_started", "run_ended"},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
