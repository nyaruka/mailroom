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
	web.InternalRoute(http.MethodPost, "/ticket/assign", web.JSONPayload(handleAssign))
}

type assignRequest struct {
	bulkTicketRequest

	AssigneeID models.UserID `json:"assignee_id"`
}

// Assigns the tickets with the given ids to the given user
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_ids": [1234, 2345],
//	  "assignee_id": 567
//	}
func handleAssign(ctx context.Context, rt *runtime.Runtime, r *assignRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	newAssignee := getOrgUser(oa, r.AssigneeID)

	scenes, err := createTicketScenes(ctx, rt, oa, r.TicketIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating scenes for tickets: %w", err)
	}

	changed := make([]*models.Ticket, 0, len(scenes))

	for _, scene := range scenes {
		for _, ticket := range scene.Tickets {
			if ticket.AssigneeID != r.AssigneeID {
				prevAssignee := getOrgUser(oa, ticket.AssigneeID)
				ticket.AssigneeID = r.AssigneeID
				evt := events.NewTicketAssigneeChanged(ticket.UUID, newAssignee.Reference(), prevAssignee.Reference())

				if err := scene.AddEvent(ctx, rt, oa, evt, r.UserID); err != nil {
					return nil, 0, fmt.Errorf("error adding assignee change event to scene: %w", err)
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

func getOrgUser(oa *models.OrgAssets, id models.UserID) *models.User {
	if id != models.NilUserID {
		return oa.UserByID(id)
	}
	return nil
}
