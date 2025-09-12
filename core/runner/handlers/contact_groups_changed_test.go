package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactGroupsChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	doctors := assets.NewGroupReference(testdb.DoctorsGroup.UUID, "Doctors")
	testers := assets.NewGroupReference(testdb.TestersGroup.UUID, "Testers")

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewAddContactGroups(flows.NewActionUUID(), []*assets.GroupReference{doctors}),
					actions.NewAddContactGroups(flows.NewActionUUID(), []*assets.GroupReference{doctors}),
					actions.NewRemoveContactGroups(flows.NewActionUUID(), []*assets.GroupReference{doctors}, false),
					actions.NewAddContactGroups(flows.NewActionUUID(), []*assets.GroupReference{testers}),
				},
				testdb.Cat.UUID: []flows.Action{
					actions.NewRemoveContactGroups(flows.NewActionUUID(), []*assets.GroupReference{doctors}, false),
					actions.NewAddContactGroups(flows.NewActionUUID(), []*assets.GroupReference{testers}),
				},
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.Ann.ID, testdb.DoctorsGroup.ID},
					Count: 0,
				},
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.Ann.ID, testdb.TestersGroup.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.Cat.ID, testdb.TestersGroup.ID},
					Count: 1,
				},
				{
					SQL:   "select count(*) from contacts_contactgroup_contacts where contact_id = $1 and contactgroup_id = $2",
					Args:  []any{testdb.Bob.ID, testdb.TestersGroup.ID},
					Count: 0,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_groups_changed", "contact_groups_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "run_ended"},
				testdb.Cat.UUID: {"run_started", "contact_groups_changed", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
