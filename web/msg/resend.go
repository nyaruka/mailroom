package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/resend", web.JSONPayload(handleResend))
}

// Request to resend failed messages.
//
//	{
//	  "org_id": 1,
//	  "msg_uuids": ["0199bada-2b39-7cac-9714-827df9ec6b91", "0199bb09-f0e9-7489-a58e-69304a7941a0"]
//	}
type resendRequest struct {
	OrgID    models.OrgID      `json:"org_id"    validate:"required"`
	MsgUUIDs []flows.EventUUID `json:"msg_uuids" validate:"required"`
}

// handles a request to resend the given messages
func handleResend(ctx context.Context, rt *runtime.Runtime, r *resendRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, r.OrgID, models.DirectionOut, r.MsgUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading messages to resend: %w", err)
	}

	resends, err := models.PrepareMessagesForResend(ctx, rt, oa, msgs)
	if err != nil {
		return nil, 0, fmt.Errorf("error resending messages: %w", err)
	}

	msgio.QueueMessages(ctx, rt, resends)

	// response is the UUIDs of the messages that were actually resent
	resentUUIDs := make([]flows.EventUUID, len(resends))
	for i, s := range resends {
		resentUUIDs[i] = s.UUID()
	}
	return map[string]any{"msg_uuids": resentUUIDs}, http.StatusOK, nil
}
