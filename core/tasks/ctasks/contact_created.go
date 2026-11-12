package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const TypeContactCreated = "contact_created"

func init() {
	RegisterType(TypeContactCreated, func() Task { return &ContactCreated{} })
}

// ContactCreated is queued for a contact created outside of message or event handling (e.g. by a channel
// registering a contact) so that its query based groups are evaluated and it's counted.
type ContactCreated struct {
	ChannelID models.ChannelID `json:"channel_id,omitempty"`
}

func (t *ContactCreated) Type() string {
	return TypeContactCreated
}

func (t *ContactCreated) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating engine contact: %w", err)
	}

	scene := runner.NewScene(mc, contact)

	if err := scene.ReevaluateGroups(ctx, rt, oa); err != nil {
		return fmt.Errorf("error calculating groups for new contact: %w", err)
	}

	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene: %w", err)
	}

	countCreatedContact(ctx, rt, oa, mc)

	return nil
}
