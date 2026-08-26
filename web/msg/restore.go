package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/restore", web.JSONPayload(handleRestore))
}

// Restores (i.e. un-archives) the given incoming messages. Messages which aren't currently archived are ignored.
//
//	{
//	  "org_id": 1,
//	  "msg_uuids": ["0199bada-2b39-7cac-9714-827df9ec6b91", "0199bb09-f0e9-7489-a58e-69304a7941a0"]
//	}
type restoreRequest struct {
	OrgID    models.OrgID       `json:"org_id"    validate:"required"`
	MsgUUIDs []events.EventUUID `json:"msg_uuids" validate:"required"`
}

func handleRestore(ctx context.Context, rt *runtime.Runtime, r *restoreRequest) (any, int, error) {
	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, r.OrgID, models.DirectionIn, r.MsgUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading messages to restore: %w", err)
	}

	if err := models.RestoreMessages(ctx, rt.DB, msgs); err != nil {
		return nil, 0, fmt.Errorf("error restoring messages: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
