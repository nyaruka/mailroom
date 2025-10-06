package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/delete", web.JSONPayload(handleDelete))
}

// Deletes the given incoming messages.
//
//	{
//	  "org_id": 1,
//	  "msg_uuids": ["0199bada-2b39-7cac-9714-827df9ec6b91", "0199bb09-f0e9-7489-a58e-69304a7941a0"]
//	}
type deleteRequest struct {
	OrgID    models.OrgID      `json:"org_id"    validate:"required"`
	MsgUUIDs []flows.EventUUID `json:"msg_uuids" validate:"required"`
}

func handleDelete(ctx context.Context, rt *runtime.Runtime, r *deleteRequest) (any, int, error) {
	if err := models.DeleteMessages(ctx, rt, r.OrgID, r.MsgUUIDs, models.VisibilityDeletedByUser); err != nil {
		return nil, 0, fmt.Errorf("error deleting messages by user: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
