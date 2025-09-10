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
//	  "ticket_uuids": ["01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"],
//	  "assignee_id": 567
//	}
func handleAssign(ctx context.Context, rt *runtime.Runtime, r *assignRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	var user *flows.User
	if r.AssigneeID != models.NilUserID {
		if u := oa.UserByID(r.AssigneeID); u != nil {
			user = oa.SessionAssets().Users().Get(u.UUID())
		}
	}

	mod := modifiers.NewTicketAssignee(r.TicketUUIDs, user)

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
			case *events.TicketAssigneeChanged:
				changed = append(changed, typed.TicketUUID)
			}
		}
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, 0, fmt.Errorf("error committing scenes for tickets: %w", err)
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
