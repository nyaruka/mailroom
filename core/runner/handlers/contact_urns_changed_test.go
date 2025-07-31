package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactURNsChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// add a URN to george that cathy will steal
	testdb.InsertContactURN(rt, testdb.Org1, testdb.George, urns.URN("tel:+12065551212"), 100, nil)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewAddContactURN(handlers.NewActionUUID(), "tel", "12065551212"),
					actions.NewAddContactURN(handlers.NewActionUUID(), "tel", "12065551212"),
					actions.NewAddContactURN(handlers.NewActionUUID(), "telegram", "11551"),
					actions.NewAddContactURN(handlers.NewActionUUID(), "tel", "+16055741111"),
				},
				testdb.George: []flows.Action{},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from contacts_contacturn where contact_id = $1 and scheme = 'telegram' and path = '11551' and priority = 998",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contacturn where contact_id = $1 and scheme = 'tel' and path = '+12065551212' and priority = 999 and identity = 'tel:+12065551212'",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contacturn where contact_id = $1 and scheme = 'tel' and path = '+16055741111' and priority = 1000",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
				// evan lost his 206 URN
				{
					SQL:   "select count(*) from contacts_contacturn where contact_id = $1",
					Args:  []any{testdb.George.ID},
					Count: 1,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
