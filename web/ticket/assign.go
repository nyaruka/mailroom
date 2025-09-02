package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tickets"
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

	mod := tickets.NewAssignmentModifier(oa, r.AssigneeID)

	scenes, err := createTicketScenes(ctx, rt, oa, r.TicketIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating scenes for tickets: %w", err)
	}

	changed := make([]*models.Ticket, 0, len(scenes))

	for _, scene := range scenes {
		chg, err := ApplyTicketModifier(ctx, rt, oa, scene, mod, r.UserID)
		if err != nil {
			return nil, 0, fmt.Errorf("error applying ticket modifier to scene: %w", err)
		}

		changed = append(changed, chg...)
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, 0, fmt.Errorf("error committing scenes for tickets: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
