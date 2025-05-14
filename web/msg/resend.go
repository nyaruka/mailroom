package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/msg/resend", web.RequireAuthToken(web.JSONPayload(handleResend)))
}

// Request to resend failed messages.
//
//	{
//	  "org_id": 1,
//	  "msg_ids": [123456, 345678]
//	}
type resendRequest struct {
	OrgID  models.OrgID   `json:"org_id"   validate:"required"`
	MsgIDs []models.MsgID `json:"msg_ids"  validate:"required"`
}

// handles a request to resend the given messages
func handleResend(ctx context.Context, rt *runtime.Runtime, r *resendRequest) (any, int, error) {
	// grab our org
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	msgs, err := models.GetMessagesByID(ctx, rt.DB, r.OrgID, models.DirectionOut, r.MsgIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading messages to resend: %w", err)
	}

	resends, err := models.ResendMessages(ctx, rt, oa, msgs)
	if err != nil {
		return nil, 0, fmt.Errorf("error resending messages: %w", err)
	}

	sends := make([]*msgio.Send, len(resends))
	for i, msg := range resends {
		sends[i] = &msgio.Send{Msg: msg}
	}

	msgio.QueueMessages(ctx, rt, sends)

	// response is the ids of the messages that were actually resent
	resentMsgIDs := make([]models.MsgID, len(resends))
	for i, m := range resends {
		resentMsgIDs[i] = m.ID()
	}
	return map[string]any{"msg_ids": resentMsgIDs}, http.StatusOK, nil
}
