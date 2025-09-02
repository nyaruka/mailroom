package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/ticket/close", web.JSONPayload(handleClose))
}

type closeRequest struct {
	bulkTicketRequest
}

// Closes any open tickets with the given ids.
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_ids": [1234, 2345]
//	}
func handleClose(ctx context.Context, rt *runtime.Runtime, r *closeRequest) (any, int, error) {
	// grab our org assets
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	tickets, err := models.LoadTickets(ctx, rt.DB, r.TicketIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading tickets for org: %d: %w", r.OrgID, err)
	}

	evts, err := models.CloseTickets(ctx, rt, oa, r.UserID, tickets)
	if err != nil {
		return nil, 0, fmt.Errorf("error closing tickets: %w", err)
	}

	for t, e := range evts {
		if e.Type == models.TicketEventTypeClosed {
			err = realtime.QueueTask(ctx, rt, e.OrgID, e.ContactID, ctasks.NewTicketClosed(t.ID))
			if err != nil {
				return nil, 0, fmt.Errorf("error queueing ticket closed task %d: %w", t.ID, err)
			}
		}
	}

	return newLegacyBulkResponse(evts), http.StatusOK, nil
}
