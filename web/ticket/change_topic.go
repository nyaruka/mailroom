package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
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

	topic := oa.TopicByID(r.TopicID)
	if topic == nil {
		return nil, 0, fmt.Errorf("no such topic with id: %d", r.TopicID)
	}

	scenes, err := createTicketScenes(ctx, rt, oa, r.TicketIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating scenes for tickets: %w", err)
	}

	changed := make([]*models.Ticket, 0, len(scenes))

	for _, scene := range scenes {
		for _, ticket := range scene.Tickets {
			if ticket.TopicID() != r.TopicID {
				if err := scene.AddEvent(ctx, rt, oa, events.NewTicketTopicChanged(ticket.UUID(), topic.Reference()), r.UserID); err != nil {
					return nil, 0, fmt.Errorf("error adding topic change event to scene: %w", err)
				}

				changed = append(changed, ticket)
			}
		}
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, 0, fmt.Errorf("error committing scenes for tickets: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
