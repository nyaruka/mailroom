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
//	  "ticket_uuids": ["01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"],
//	  "note": "spam"
//	}
func handleAddNote(ctx context.Context, rt *runtime.Runtime, r *addNoteRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	mod := modifiers.NewTicketNote(r.TicketUUIDs, r.Note)

	scenes, err := createTicketScenes(ctx, rt, oa, r.TicketIDs)
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
			case *events.TicketNoteAdded:
				changed = append(changed, typed.TicketUUID)
			}
		}
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, 0, fmt.Errorf("error committing scenes for tickets: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
