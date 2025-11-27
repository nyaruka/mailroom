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
	web.InternalRoute(http.MethodPost, "/ticket/close", web.JSONPayload(handleClose))
}

type closeRequest struct {
	bulkTicketRequest
}

// Closes the specified tickets if they're open.
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_uuids": ["01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"],
//	}
func handleClose(ctx context.Context, rt *runtime.Runtime, r *closeRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	mod := func(t *models.Ticket) flows.Modifier {
		return modifiers.NewTicketClose(t.UUID)
	}

	eventsByContact, err := modifyTickets(ctx, rt, oa, r.UserID, r.TicketUUIDs, mod)
	if err != nil {
		return nil, 0, fmt.Errorf("error closing tickets: %w", err)
	}

	return newBulkResponse(eventsByContact), http.StatusOK, nil
}
