package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
)

func reindexContacts(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID) error {
	contacts, err := models.LoadContacts(ctx, rt.DB, oa, contactIDs)
	if err != nil {
		return fmt.Errorf("error loading contacts for reindex: %w", err)
	}

	flowContacts := make([]*flows.Contact, 0, len(contacts))
	currentFlows := make(map[models.ContactID]models.FlowID, len(contacts))
	for _, c := range contacts {
		fc, err := c.EngineContact(oa)
		if err != nil {
			return fmt.Errorf("error creating engine contact: %w", err)
		}
		flowContacts = append(flowContacts, fc)
		currentFlows[c.ID()] = c.CurrentFlowID()
	}

	return search.IndexContacts(ctx, rt, oa, flowContacts, currentFlows)
}
