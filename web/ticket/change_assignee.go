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
	web.InternalRoute(http.MethodPost, "/ticket/change_assignee", web.JSONPayload(handleChangeAssignee))
}

type assignRequest struct {
	bulkTicketRequest

	AssigneeID models.UserID `json:"assignee_id"`
}

// Changes the assignee of the specified tickets.
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_uuids": ["01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"],
//	  "assignee_id": 567
//	}
func handleChangeAssignee(ctx context.Context, rt *runtime.Runtime, r *assignRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	var user *flows.User
	if r.AssigneeID != models.NilUserID {
		if u := oa.UserByID(r.AssigneeID); u != nil {
			user = oa.SessionAssets().Users().Get(u.UUID())
		}
	}

	mod := func(t *models.Ticket) flows.Modifier {
		return modifiers.NewTicketAssignee(t.UUID, user)
	}

	eventsByContact, err := modifyTickets(ctx, rt, oa, r.UserID, r.TicketUUIDs, mod, r.Via)
	if err != nil {
		return nil, 0, fmt.Errorf("error changing ticket assignee: %w", err)
	}

	return newBulkResponse(eventsByContact), http.StatusOK, nil
}
