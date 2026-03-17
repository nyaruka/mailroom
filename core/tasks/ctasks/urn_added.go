package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeURNAdded = "urn_added"

func init() {
	RegisterType(TypeURNAdded, func() Task { return &URNAdded{} })
}

type URNAdded struct {
	URN urns.URN `json:"urn"`
}

func (t *URNAdded) Type() string {
	return TypeURNAdded
}

func (t *URNAdded) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	urn := t.URN.Normalize()

	// if contact already has this URN, nothing to do
	if mc.FindURN(urn) != nil {
		return nil
	}

	oldOwnerID, err := models.ClaimURN(ctx, rt.DB, oa, mc.ID(), urn)
	if err != nil {
		return fmt.Errorf("error claiming urn: %w", err)
	}

	// update modified_on for affected contacts
	contactIDs := []models.ContactID{mc.ID()}
	if oldOwnerID != models.NilContactID {
		contactIDs = append(contactIDs, oldOwnerID)
	}

	if err := models.UpdateContactModifiedOn(ctx, rt.DB, contactIDs); err != nil {
		return fmt.Errorf("error updating modified_on: %w", err)
	}

	if err := reindexContacts(ctx, rt, oa, contactIDs); err != nil {
		return fmt.Errorf("error reindexing contacts: %w", err)
	}

	return nil
}

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
