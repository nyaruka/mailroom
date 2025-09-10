package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/modifiers"
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
//	  "ticket_uuids": ["01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"],
//	  "topic_id": 345
//	}
func handleChangeTopic(ctx context.Context, rt *runtime.Runtime, r *changeTopicRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	// TODO have RP pass topic by UUID to simplify this
	tp := oa.TopicByID(r.TopicID)
	topic := oa.SessionAssets().Topics().Get(tp.UUID())

	mod := modifiers.NewTicketTopic(r.TicketUUIDs, topic)

	scenes, err := createTicketScenes(ctx, rt, oa, r.TicketUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating scenes for tickets: %w", err)
	}

	changed := make([]flows.TicketUUID, 0, len(scenes))

	for _, scene := range scenes {
		evts, err := scene.ApplyModifier(ctx, rt, oa, mod, r.UserID)
		if err != nil {
			return nil, 0, fmt.Errorf("error applying ticket modifier to scene: %w", err)
		}

		for _, e := range evts {
			switch typed := e.(type) {
			case *events.TicketTopicChanged:
				changed = append(changed, typed.TicketUUID)
			}
		}
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, 0, fmt.Errorf("error committing scenes for tickets: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
