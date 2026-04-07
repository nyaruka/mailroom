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

	if t.NewURN != nil {
		if err := t.NewURN.Apply(ctx, rt, oa, scene, oa.ChannelByID(t.ChannelID)); err != nil {
			return fmt.Errorf("error applying new URN: %w", err)
		}
	}

	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene: %w", err)
	}

	return nil
}
