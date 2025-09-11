package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/ticket/change_topic", web.JSONPayload(handleChangeTopic))
}

type changeTopicRequest struct {
	bulkTicketRequest

	TopicUUID assets.TopicUUID `json:"topic_uuid" validate:"required"`
}

// Changes the topic of the specified tickets.
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_uuids": ["01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"],
//	  "topic_uuid": "7e39a04f-c7e9-4c1b-b9eb-7f3b4be5f183"
//	}
func handleChangeTopic(ctx context.Context, rt *runtime.Runtime, r *changeTopicRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	topic := oa.SessionAssets().Topics().Get(r.TopicUUID)
	if topic == nil {
		return nil, 0, fmt.Errorf("no such topic: %s", r.TopicUUID)
	}

	mod := func(t *models.Ticket) flows.Modifier {
		return modifiers.NewTicketTopic(t.UUID, topic)
	}

	changed, err := modifyTickets(ctx, rt, oa, r.UserID, r.TicketUUIDs, mod)
	if err != nil {
		return nil, 0, fmt.Errorf("error changing ticket topic: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
