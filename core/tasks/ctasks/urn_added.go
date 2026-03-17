package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
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

	claimed, err := models.ClaimURN(ctx, rt.DB, oa, mc.ID(), urn)
	if err != nil {
		return fmt.Errorf("error claiming urn: %w", err)
	}

	if !claimed {
		return nil // URN belongs to another contact, nothing to do
	}

	if err := models.UpdateContactModifiedOn(ctx, rt.DB, []models.ContactID{mc.ID()}); err != nil {
		return fmt.Errorf("error updating modified_on: %w", err)
	}

	if err := reindexContacts(ctx, rt, oa, []models.ContactID{mc.ID()}); err != nil {
		return fmt.Errorf("error reindexing contacts: %w", err)
	}

	return nil
}

