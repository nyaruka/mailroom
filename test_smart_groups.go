package main

import (
	"context"
	"fmt"
	"log"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func main() {
	ctx, rt := testsuite.Runtime()
	defer testsuite.Reset(testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	if err != nil {
		log.Fatalf("Error getting org assets: %v", err)
	}

	fmt.Println("Testing smart group recalculation on field changes...")

	// Update DoctorsGroup to be a smart group based on age > 18
	rt.DB.MustExec(`UPDATE contacts_contactgroup SET query = 'age > 18', group_type = 'Q' WHERE id = $1`, testdb.DoctorsGroup.ID)

	// Create a test contact
	contact, _, err := models.CreateContact(ctx, rt.DB, oa, models.UserID(1), "Test Contact", "eng", models.ContactStatusActive, nil)
	if err != nil {
		log.Fatalf("Error creating contact: %v", err)
	}

	// Set initial age to 25
	rt.DB.MustExec(
		fmt.Sprintf(`UPDATE contacts_contact SET fields = fields || '{"%s": {"text": "25", "number": 25}}'::jsonb WHERE id = $1`, testdb.AgeField.UUID),
		contact.ID(),
	)

	// Add contact to group manually to simulate initial evaluation
	rt.DB.MustExec(`INSERT INTO contacts_contactgroup_contacts(contactgroup_id, contact_id) VALUES($1, $2) ON CONFLICT DO NOTHING`, testdb.DoctorsGroup.ID, contact.ID())

	// Check initial group membership
	contactIDs, err := models.GetGroupContactIDs(ctx, rt.DB, testdb.DoctorsGroup.ID)
	if err != nil {
		log.Fatalf("Error getting group contact IDs: %v", err)
	}

	fmt.Printf("Contact initially in group: %v\n", contains(contactIDs, contact.ID()))

	// Refresh org assets
	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshGroups)
	if err != nil {
		log.Fatalf("Error refreshing org assets: %v", err)
	}

	// Create a session and scene for testing
	session, _, err := models.CreateSession(ctx, rt.DB, oa, testdb.TwilioChannel.ID, flows.ContactID(contact.ID()), flows.ContactURN(""), models.StartTypeTrigger, flows.FlowReference{}, "")
	if err != nil {
		log.Fatalf("Error creating session: %v", err)
	}

	scene := runner.NewScene(session, nil)

	// Get age field
	ageField := oa.FieldByKey("age")
	if ageField == nil {
		log.Fatal("Age field not found")
	}

	// Create field change event (age 25 -> 15)
	fieldChangedEvent := events.NewContactFieldChanged(ageField.AsEngineField(), flows.NewText("15"))

	// Handle the field change
	err = handlers.HandleContactFieldChanged(ctx, rt, oa, scene, fieldChangedEvent, models.UserID(1))
	if err != nil {
		log.Fatalf("Error handling field change: %v", err)
	}

	// Apply hooks
	err = scene.ApplyPreCommitHooks(ctx, rt, oa)
	if err != nil {
		log.Fatalf("Error applying hooks: %v", err)
	}

	// Check group membership after field change
	contactIDs, err = models.GetGroupContactIDs(ctx, rt.DB, testdb.DoctorsGroup.ID)
	if err != nil {
		log.Fatalf("Error getting group contact IDs after change: %v", err)
	}

	inGroupAfter := contains(contactIDs, contact.ID())
	fmt.Printf("Contact in group after field change: %v\n", inGroupAfter)

	if inGroupAfter {
		fmt.Println("❌ FAIL: Contact should have been removed from group (age 15 < 18)")
	} else {
		fmt.Println("✅ PASS: Contact correctly removed from group when age changed to 15")
	}
}

func contains(slice []models.ContactID, id models.ContactID) bool {
	for _, item := range slice {
		if item == id {
			return true
		}
	}
	return false
}