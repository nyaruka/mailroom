package handlers_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
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

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	// Use the existing DoctorsGroup and update it to be a smart group based on age > 18
	rt.DB.MustExec(`UPDATE contacts_contactgroup SET query = 'age > 18', group_type = 'Q' WHERE id = $1`, testdb.DoctorsGroup.ID)
	
	// Create a contact with age 25 (should be in the smart group)
	contact, _, err := models.CreateContact(ctx, rt.DB, oa, models.UserID(1), "Test Contact", "eng", models.ContactStatusActive, nil)
	assert.NoError(t, err)

	// Set initial age to 25 - contact should be in smart group initially
	rt.DB.MustExec(
		fmt.Sprintf(`UPDATE contacts_contact SET fields = fields || '{"%s": {"text": "25", "number": 25}}'::jsonb WHERE id = $1`, testdb.AgeField.UUID),
		contact.ID(),
	)

	// Refresh org assets and manually populate the smart group to establish initial membership
	testsuite.ReindexElastic(ctx)
	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshGroups)
	assert.NoError(t, err)

	// Add the contact to the group manually to simulate initial smart group evaluation
	rt.DB.MustExec(`INSERT INTO contacts_contactgroup_contacts(contactgroup_id, contact_id) VALUES($1, $2) ON CONFLICT DO NOTHING`, testdb.DoctorsGroup.ID, contact.ID())

	// Verify contact is initially in the group
	contactIDs, err := models.GetGroupContactIDs(ctx, rt.DB, testdb.DoctorsGroup.ID)
	assert.NoError(t, err)
	assert.Contains(t, contactIDs, contact.ID(), "Contact should initially be in the group")

	// Now simulate a field change using the handlers test framework
	ageField := oa.FieldByKey("age")
	assert.NotNil(t, ageField, "Age field should exist")

	// Test case that changes age from 25 to 15 (should remove from group)
	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				&testdb.Contact{contact.ID(), "", "", models.NilURNID}: []flows.Action{
					actions.NewSetContactField(handlers.NewActionUUID(), ageField.AsEngineField().Reference(), "15"),
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)

	// After the field change, check if smart group recalculation happened
	// The contact should no longer be in the group since age is now 15 (< 18)
	contactIDs, err = models.GetGroupContactIDs(ctx, rt.DB, testdb.DoctorsGroup.ID)
	assert.NoError(t, err)
	
	// This assertion should pass after implementing the fix for smart group recalculation
	assert.NotContains(t, contactIDs, contact.ID(), "Contact should be removed from group when age changes to 15")
}
