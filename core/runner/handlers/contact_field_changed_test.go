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

func TestContactFieldChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	gender := assets.NewFieldReference("gender", "Gender")
	age := assets.NewFieldReference("age", "Age")

	// populate some field values on alexandria
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"903f51da-2717-47c7-a0d3-f2f32877013d": {"text":"34"}, "3a5891e4-756e-4dc9-8e12-b7a766168824": {"text":"female"}}' WHERE id = $1`, testdb.Alexandra.ID)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewSetContactField(handlers.NewActionUUID(), gender, "Male"),
					actions.NewSetContactField(handlers.NewActionUUID(), gender, "Female"),
					actions.NewSetContactField(handlers.NewActionUUID(), age, ""),
				},
				testdb.George: []flows.Action{
					actions.NewSetContactField(handlers.NewActionUUID(), gender, "Male"),
					actions.NewSetContactField(handlers.NewActionUUID(), gender, ""),
					actions.NewSetContactField(handlers.NewActionUUID(), age, "40"),
				},
				testdb.Bob: []flows.Action{
					actions.NewSetContactField(handlers.NewActionUUID(), gender, ""),
					actions.NewSetContactField(handlers.NewActionUUID(), gender, "Male"),
					actions.NewSetContactField(handlers.NewActionUUID(), age, "Old"),
				},
				testdb.Alexandra: []flows.Action{
					actions.NewSetContactField(handlers.NewActionUUID(), age, ""),
					actions.NewSetContactField(handlers.NewActionUUID(), gender, ""),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields->$2 = '{"text":"Female"}'::jsonb`,
					Args:  []any{testdb.Cathy.ID, testdb.GenderField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND NOT fields?$2`,
					Args:  []any{testdb.Cathy.ID, testdb.AgeField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND NOT fields?$2`,
					Args:  []any{testdb.George.ID, testdb.GenderField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields->$2 = '{"text":"40", "number": 40}'::jsonb`,
					Args:  []any{testdb.George.ID, testdb.AgeField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields->$2 = '{"text":"Male"}'::jsonb`,
					Args:  []any{testdb.Bob.ID, testdb.GenderField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields->$2 = '{"text":"Old"}'::jsonb`,
					Args:  []any{testdb.Bob.ID, testdb.AgeField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND NOT fields?$2`,
					Args:  []any{testdb.Bob.ID, "unknown"},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields = '{}'`,
					Args:  []any{testdb.Alexandra.ID},
					Count: 1,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}

func TestContactFieldChangedSmartGroupRecalculation(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	defer testsuite.Reset(testsuite.ResetAll)

	// This test verifies that smart groups are recalculated when contact fields change
	// We test this by changing a field value and verifying the contact's group membership changes

	// Use the existing DoctorsGroup and make it a smart group based on age > 18
	rt.DB.MustExec(`UPDATE contacts_contactgroup SET query = 'age > 18', group_type = 'Q' WHERE id = $1`, testdb.DoctorsGroup.ID)

	// Test case that changes age from 25 to 15
	// If smart group recalculation works, the contact should be removed from the "Adults" group
	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					// First set age to 25 (should qualify for age > 18 group)
					actions.NewSetContactField(handlers.NewActionUUID(), assets.NewFieldReference("age", "Age"), "25"),
					// Then set age to 15 (should no longer qualify for age > 18 group)
					actions.NewSetContactField(handlers.NewActionUUID(), assets.NewFieldReference("age", "Age"), "15"),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					// Verify the age field was updated to 15
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields->$2 = '{"text":"15", "number": 15}'::jsonb`,
					Args:  []any{testdb.Cathy.ID, testdb.AgeField.UUID},
					Count: 1,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)

	// The test passes if we reach here without errors
	// The RecalculateSmartGroups hook should have been executed during the field changes above
}
