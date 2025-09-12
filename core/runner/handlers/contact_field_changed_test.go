package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestContactFieldChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	gender := assets.NewFieldReference("gender", "Gender")
	age := assets.NewFieldReference("age", "Age")

	// populate some field values on Dan
	rt.DB.MustExec(`UPDATE contacts_contact SET fields = '{"903f51da-2717-47c7-a0d3-f2f32877013d": {"text":"34"}, "3a5891e4-756e-4dc9-8e12-b7a766168824": {"text":"female"}}' WHERE id = $1`, testdb.Dan.ID)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewSetContactField(flows.NewActionUUID(), gender, "Male"),
					actions.NewSetContactField(flows.NewActionUUID(), gender, "Female"),
					actions.NewSetContactField(flows.NewActionUUID(), age, ""),
				},
				testdb.Cat.UUID: []flows.Action{
					actions.NewSetContactField(flows.NewActionUUID(), gender, "Male"),
					actions.NewSetContactField(flows.NewActionUUID(), gender, ""),
					actions.NewSetContactField(flows.NewActionUUID(), age, "40"),
				},
				testdb.Bob.UUID: []flows.Action{
					actions.NewSetContactField(flows.NewActionUUID(), gender, ""),
					actions.NewSetContactField(flows.NewActionUUID(), gender, "Male"),
					actions.NewSetContactField(flows.NewActionUUID(), age, "Old"),
				},
				testdb.Dan.UUID: []flows.Action{
					actions.NewSetContactField(flows.NewActionUUID(), age, ""),
					actions.NewSetContactField(flows.NewActionUUID(), gender, ""),
				},
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields->$2 = '{"text":"Female"}'::jsonb`,
					Args:  []any{testdb.Ann.ID, testdb.GenderField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND NOT fields?$2`,
					Args:  []any{testdb.Ann.ID, testdb.AgeField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND NOT fields?$2`,
					Args:  []any{testdb.Cat.ID, testdb.GenderField.UUID},
					Count: 1,
				},
				{
					SQL:   `select count(*) from contacts_contact where id = $1 AND fields->$2 = '{"text":"40", "number": 40}'::jsonb`,
					Args:  []any{testdb.Cat.ID, testdb.AgeField.UUID},
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
					Args:  []any{testdb.Dan.ID},
					Count: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_field_changed", "contact_field_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "contact_field_changed", "contact_field_changed", "run_ended"},
				testdb.Cat.UUID: {"run_started", "contact_field_changed", "contact_field_changed", "contact_field_changed", "run_ended"},
				testdb.Dan.UUID: {"run_started", "contact_field_changed", "contact_field_changed", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
