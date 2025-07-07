package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/ticket/close", web.RequireAuthToken(web.MarshaledResponse(handleClose)))
}

// Closes any open tickets with the given ids.
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_ids": [1234, 2345]
//	}
func handleClose(ctx context.Context, rt *runtime.Runtime, r *http.Request) (any, int, error) {
	request := &bulkTicketRequest{}
	if err := web.ReadAndValidateJSON(r, request); err != nil {
		return fmt.Errorf("request failed validation: %w", err), http.StatusBadRequest, nil
	}

	// grab our org assets
	oa, err := models.GetOrgAssets(ctx, rt, request.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	tickets, err := models.LoadTickets(ctx, rt.DB, request.TicketIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading tickets for org: %d: %w", request.OrgID, err)
	}

	evts, err := models.CloseTickets(ctx, rt, oa, request.UserID, tickets)
	if err != nil {
		return nil, 0, fmt.Errorf("error closing tickets: %w", err)
	}

	rc := rt.VK.Get()
	defer rc.Close()

	for t, e := range evts {
		if e.EventType() == models.TicketEventTypeClosed {
			err = handler.QueueTask(ctx, rc, e.OrgID(), e.ContactID(), ctasks.NewTicketClosed(t.ID()))
			if err != nil {
				return nil, 0, fmt.Errorf("error queueing ticket closed task %d: %w", t.ID(), err)
			}
		}
	}

	return newBulkResponse(evts), http.StatusOK, nil
}
