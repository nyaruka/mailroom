package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const TypeContactChanged = "contact_changed"

func init() {
	RegisterType(TypeContactChanged, func() Task { return &ContactChanged{} })
}

type ContactChanged struct {
	ChannelID models.ChannelID `json:"channel_id"`
	NewURN    *NewURNSpec      `json:"new_urn,omitempty"`
}

func (t *ContactChanged) Type() string {
	return TypeContactChanged
}

func (t *ContactChanged) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	scene := runner.NewScene(mc, contact)

	var newURNAdded bool
	if t.NewURN != nil {
		added, err := t.NewURN.Apply(ctx, rt, oa, scene, oa.ChannelByID(t.ChannelID))
		if err != nil {
			return fmt.Errorf("error applying new URN: %w", err)
		}
		newURNAdded = added
	}

	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene: %w", err)
	}

	if newURNAdded {
		if err := t.NewURN.EnsureChannel(ctx, rt.DB, mc.ID(), t.ChannelID); err != nil {
			return fmt.Errorf("error ensuring channel affinity on new URN: %w", err)
		}
	}

	return nil
}
