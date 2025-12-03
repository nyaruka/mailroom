package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
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
//	  "msg_uuids": ["0199bada-2b39-7cac-9714-827df9ec6b91", "0199bb09-f0e9-7489-a58e-69304a7941a0"]
//	}
type handleRequest struct {
	OrgID    models.OrgID      `json:"org_id"  validate:"required"`
	MsgUUIDs []flows.EventUUID `json:"msg_uuids" validate:"required"`
}

// handles a request to resend the given messages
func handleHandle(ctx context.Context, rt *runtime.Runtime, r *handleRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, oa.OrgID(), models.DirectionIn, r.MsgUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading messages to handle: %w", err)
	}

	// response is the ids of the messages that were actually queued
	queuedMsgUUIDs := make([]flows.EventUUID, 0, len(r.MsgUUIDs))

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

		queuedMsgUUIDs = append(queuedMsgUUIDs, m.UUID())
	}

	return map[string]any{"msg_uuids": queuedMsgUUIDs}, http.StatusOK, nil
}
