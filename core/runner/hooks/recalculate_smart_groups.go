package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// RecalculateSmartGroups is our hook for recalculating smart groups when contact fields change
var RecalculateSmartGroups runner.PreCommitHook = &recalculateSmartGroups{}

type recalculateSmartGroups struct{}

func (h *recalculateSmartGroups) Order() int { return 2 } // run after field updates but before modified_on

func (h *recalculateSmartGroups) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// collect all contacts that had field changes
	contactIDs := make([]models.ContactID, 0, len(scenes))
	for scene := range scenes {
		contactIDs = append(contactIDs, scene.ContactID())
	}

	if len(contactIDs) == 0 {
		return nil
	}

	// load the contacts that had field changes
	contacts, err := models.LoadContacts(ctx, tx, oa, contactIDs)
	if err != nil {
		return fmt.Errorf("error loading contacts for smart group recalculation: %w", err)
	}

	// convert to flow contacts for smart group evaluation
	flowContacts := make([]*flows.Contact, len(contacts))
	for i, contact := range contacts {
		flowContact, err := contact.EngineContact(oa)
		if err != nil {
			return fmt.Errorf("error converting contact to flow contact for smart group recalculation: %w", err)
		}
		flowContacts[i] = flowContact
	}

	// recalculate smart groups for these contacts
	err = models.CalculateDynamicGroups(ctx, tx, oa, flowContacts)
	if err != nil {
		return fmt.Errorf("error recalculating smart groups after field changes: %w", err)
	}

	return nil
}
