package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/handle", web.JSONPayload(handleHandle))
}

// Queues the given incoming messages for handling. This is only used for recovering from failures where we might need
// to manually retry handling of a message.
//
//	{
//	  "org_id": 1,
//	  "msg_ids": [12345, 23456]
//	}
type handleRequest struct {
	OrgID  models.OrgID   `json:"org_id"  validate:"required"`
	MsgIDs []models.MsgID `json:"msg_ids" validate:"required"`
}

// handles a request to resend the given messages
func handleHandle(ctx context.Context, rt *runtime.Runtime, r *handleRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	msgs, err := models.GetMessagesByID(ctx, rt.DB, oa.OrgID(), models.DirectionIn, r.MsgIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading messages to handle: %w", err)
	}

	// response is the ids of the messages that were actually queued
	queuedMsgIDs := make([]models.MsgID, 0, len(r.MsgIDs))

	for _, m := range msgs {
		if m.Status() != models.MsgStatusPending || m.ContactURNID() == models.NilURNID {
			continue
		}

		cu, err := models.LoadContactURN(ctx, rt.DB, m.ContactURNID())
		if err != nil {
			return nil, 0, fmt.Errorf("error fetching msg URN: %w", err)
		}

		attachments := make([]string, len(m.Attachments()))
		for i := range m.Attachments() {
			attachments[i] = string(m.Attachments()[i])
		}

		urn, _ := cu.Encode(oa)

		err = realtime.QueueTask(ctx, rt, m.OrgID(), m.ContactID(), &ctasks.MsgReceivedTask{
			ChannelID:     m.ChannelID(),
			MsgID:         m.ID(),
			MsgUUID:       m.UUID(),
			MsgExternalID: m.ExternalID(),
			URN:           urn,
			URNID:         m.ContactURNID(),
			Text:          m.Text(),
			Attachments:   attachments,
			NewContact:    false,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("error queueing handle task: %w", err)
		}

		queuedMsgIDs = append(queuedMsgIDs, m.ID())
	}

	return map[string]any{"msg_ids": queuedMsgIDs}, http.StatusOK, nil
}
