package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeMsgDeleted = "msg_deleted"

func init() {
	realtime.RegisterContactTask(TypeMsgDeleted, func() realtime.Task { return &MsgDeletedTask{} })
}

type MsgDeletedTask struct {
	MsgID models.MsgID `json:"message_id"`
}

func (t *MsgDeletedTask) Type() string {
	return TypeMsgDeleted
}

func (t *MsgDeletedTask) UseReadOnly() bool {
	return true
}

func (t *MsgDeletedTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	err := models.UpdateMessageDeletedBySender(ctx, rt.DB.DB, oa.OrgID(), t.MsgID)
	if err != nil {
		return fmt.Errorf("error deleting message: %w", err)
	}
	return nil
}
