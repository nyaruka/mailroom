package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
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
	err := models.DeleteMessages(ctx, rt, oa, []flows.EventUUID{t.MsgUUID}, models.VisibilityDeletedBySender, models.NilUserID)
	if err != nil {
		return fmt.Errorf("error deleting message by sender: %w", err)
	}
	return nil
}
