package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/models"
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

// Adds the given text note to the specified tickets.
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

	mod := func(t *models.Ticket) flows.Modifier {
		return modifiers.NewTicketNote(t.UUID, r.Note)
	}

	changed, err := modifyTickets(ctx, rt, oa, r.UserID, r.TicketUUIDs, mod)
	if err != nil {
		return nil, 0, fmt.Errorf("error adding note to tickets: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
