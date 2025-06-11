package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactGroupsChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	doctors := assets.NewGroupReference(testdb.DoctorsGroup.UUID, "Doctors")
	testers := assets.NewGroupReference(testdb.TestersGroup.UUID, "Testers")

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewAddContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}),
					actions.NewAddContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}),
					actions.NewRemoveContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}, false),
					actions.NewAddContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{testers}),
				},
				testdb.George: []flows.Action{
					actions.NewRemoveContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{doctors}, false),
					actions.NewAddContactGroups(handlers.NewActionUUID(), []*assets.GroupReference{testers}),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.Cathy.ID, testdb.DoctorsGroup.ID},
					Count: 0,
				},
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.Cathy.ID, testdb.TestersGroup.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.George.ID, testdb.TestersGroup.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.Bob.ID, testdb.TestersGroup.ID},
					Count: 0,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
