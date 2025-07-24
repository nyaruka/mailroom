package hooks

import (
	"context"
	"testing"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestRecalculateSmartGroups(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	defer testsuite.Reset(testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	// Test that the hook executes without error
	hook := &recalculateSmartGroups{}
	assert.Equal(t, 2, hook.Order(), "Hook should have order 2")

	// Create a test contact
	contact, _, err := models.CreateContact(ctx, rt.DB, oa, models.UserID(1), "Test Contact", "eng", models.ContactStatusActive, nil)
	assert.NoError(t, err)

	// Create a mock session and scene
	session, _, err := models.CreateSession(ctx, rt.DB, oa, testdb.TwilioChannel.ID, flows.ContactID(contact.ID()), flows.ContactURN(""), models.StartTypeTrigger, flows.FlowReference{}, "")
	assert.NoError(t, err)

	scene := runner.NewScene(session, nil)

	// Create a mock field changed event
	ageField := oa.FieldByKey("age")
	if ageField != nil {
		fieldChangedEvent := events.NewContactFieldChanged(ageField.AsEngineField(), flows.NewText("25"))

		// Test that the hook can execute without error
		scenes := map[*runner.Scene][]any{
			scene: {fieldChangedEvent},
		}

		err = hook.Execute(ctx, rt, rt.DB, oa, scenes)
		assert.NoError(t, err, "RecalculateSmartGroups hook should execute without error")
	}
}
