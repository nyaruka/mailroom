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
	UserID   models.UserID     `json:"user_id"   validate:"required"`
	MsgUUIDs []flows.EventUUID `json:"msg_uuids" validate:"required"`
}

func handleDelete(ctx context.Context, rt *runtime.Runtime, r *deleteRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	if err := models.DeleteMessages(ctx, rt, oa, r.MsgUUIDs, models.VisibilityDeletedByUser, r.UserID); err != nil {
		return nil, 0, fmt.Errorf("error deleting messages by user: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
