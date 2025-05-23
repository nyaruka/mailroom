package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
)

func TestContactLanguageChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdata.Cathy: []flows.Action{
					actions.NewSetContactLanguage(handlers.NewActionUUID(), "fra"),
					actions.NewSetContactLanguage(handlers.NewActionUUID(), "eng"),
				},
				testdata.George: []flows.Action{
					actions.NewSetContactLanguage(handlers.NewActionUUID(), "spa"),
				},
				testdata.Alexandria: []flows.Action{
					actions.NewSetContactLanguage(handlers.NewActionUUID(), ""),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language = 'eng'",
					Args:  []any{testdata.Cathy.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language = 'spa'",
					Args:  []any{testdata.George.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language is NULL;",
					Args:  []any{testdata.Bob.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contact where id = $1 and language is NULL;",
					Args:  []any{testdata.Alexandria.ID},
					Count: 1,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
