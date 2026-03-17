package ctasks

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeURNRemoved = "urn_removed"

func init() {
	RegisterType(TypeURNRemoved, func() Task { return &URNRemoved{} })
}

type URNRemoved struct {
	URN urns.URN `json:"urn"`
}

func (t *URNRemoved) Type() string {
	return TypeURNRemoved
}

func (t *URNRemoved) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	urn := t.URN.Normalize()

	// if contact doesn't have this URN, nothing to do
	if mc.FindURN(urn) == nil {
		return nil
	}

	// don't remove the last URN
	if len(mc.URNs()) <= 1 {
		slog.Warn("ignoring urn_removed task, would remove last URN", "contact_id", mc.ID(), "urn", urn.Identity())
		return nil
	}

	if err := models.DetachContactURN(ctx, rt.DB, oa.OrgID(), mc.ID(), urn.Identity()); err != nil {
		return fmt.Errorf("error detaching urn: %w", err)
	}

	if err := models.UpdateContactModifiedOn(ctx, rt.DB, []models.ContactID{mc.ID()}); err != nil {
		return fmt.Errorf("error updating modified_on: %w", err)
	}

	if err := reindexContacts(ctx, rt, oa, []models.ContactID{mc.ID()}); err != nil {
		return fmt.Errorf("error reindexing contact: %w", err)
	}

	return nil
}
