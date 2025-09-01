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
	web.InternalRoute(http.MethodPost, "/ticket/add_note", web.JSONPayload(handleAddNote))
}

type addNoteRequest struct {
	bulkTicketRequest

	Note string `json:"note" validate:"required"`
}

// Adds the given text note to the tickets with the given ids
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_ids": [1234, 2345],
//	  "note": "spam"
//	}
func handleAddNote(ctx context.Context, rt *runtime.Runtime, r *addNoteRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	scenes, err := createTicketScenes(ctx, rt, oa, r.TicketIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating scenes for tickets: %w", err)
	}

	changed := make([]*models.Ticket, 0, len(scenes))

	for _, scene := range scenes {
		for _, ticket := range scene.Tickets {
			if err := scene.AddEvent(ctx, rt, oa, events.NewTicketNoteAdded(ticket.UUID(), r.Note), r.UserID); err != nil {
				return nil, 0, fmt.Errorf("error adding note added event to scene: %w", err)
			}

			changed = append(changed, ticket)
		}
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, 0, fmt.Errorf("error committing scenes for tickets: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
