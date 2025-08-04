package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/ticket/change_topic", web.JSONPayload(handleChangeTopic))
}

type changeTopicRequest struct {
	bulkTicketRequest

	TopicID models.TopicID `json:"topic_id" validate:"required"`
}

// Changes the topic of the tickets with the given ids
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_ids": [1234, 2345],
//	  "topic_id": 345
//	}
func handleChangeTopic(ctx context.Context, rt *runtime.Runtime, r *changeTopicRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	tickets, err := models.LoadTickets(ctx, rt.DB, r.TicketIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading tickets for org: %d: %w", r.OrgID, err)
	}

	evts, err := models.TicketsChangeTopic(ctx, rt.DB, oa, r.UserID, tickets, r.TopicID)
	if err != nil {
		return nil, 0, fmt.Errorf("error changing topic of tickets: %w", err)
	}

	return newBulkResponse(evts), http.StatusOK, nil
}
