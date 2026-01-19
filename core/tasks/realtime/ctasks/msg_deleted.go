package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeMsgDeleted = "msg_deleted"

func init() {
	realtime.RegisterContactTask(TypeMsgDeleted, func() realtime.Task { return &MsgDeletedTask{} })
}

type MsgDeletedTask struct {
	MsgUUID flows.EventUUID `json:"msg_uuid"`
}

func (t *MsgDeletedTask) Type() string {
	return TypeMsgDeleted
}

func (t *MsgDeletedTask) UseReadOnly() bool {
	return true
}

func (t *MsgDeletedTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	// build our flow contact
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	scene := runner.NewScene(mc, contact)

	evt := events.NewMsgDeleted(t.MsgUUID, true)

	if err := scene.AddEvent(ctx, rt, oa, evt, models.NilUserID, ""); err != nil {
		return fmt.Errorf("error adding msg delete event to scene for contact %s: %w", scene.ContactUUID(), err)
	}

	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene for contact %s: %w", scene.ContactUUID(), err)
	}

	return nil
}
