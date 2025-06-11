package ticket

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/clocks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/ticket/reopen", web.RequireAuthToken(web.MarshaledResponse(handleReopen)))
}

// Reopens any closed tickets with the given ids
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_ids": [1234, 2345]
//	}
func handleReopen(ctx context.Context, rt *runtime.Runtime, r *http.Request) (any, int, error) {
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

	// organize last opened ticket by contact (we know we can't open more than one ticket per contact)
	ticketByContact := make(map[models.ContactID]*models.Ticket, len(request.TicketIDs))
	for _, t := range tickets {
		if ticketByContact[t.ContactID()] == nil {
			ticketByContact[t.ContactID()] = t
		}
	}

	results := make(map[*models.Ticket]*models.TicketEvent, len(tickets))
	remaining := ticketByContact
	start := time.Now()

	for len(remaining) > 0 && time.Since(start) < time.Second*10 {
		evts, skipped, err := tryToLockAndReopen(ctx, rt, oa, remaining, request.UserID)
		if err != nil {
			return nil, 0, err
		}

		maps.Copy(results, evts)

		remaining = skipped
	}

	return newBulkResponse(results), http.StatusOK, nil
}

func tryToLockAndReopen(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, tickets map[models.ContactID]*models.Ticket, userID models.UserID) (map[*models.Ticket]*models.TicketEvent, map[models.ContactID]*models.Ticket, error) {
	locks, skipped, err := clocks.TryToLock(ctx, rt, oa, slices.Collect(maps.Keys(tickets)), time.Second)
	if err != nil {
		return nil, nil, err
	}

	locked := slices.Collect(maps.Keys(locks))

	defer clocks.Unlock(ctx, rt, oa, locks)

	// load our contacts
	contacts, err := models.LoadContacts(ctx, rt.DB, oa, locked)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to load contacts: %w", err)
	}

	// filter tickets to those belonging to contacts without an open ticket
	reopenable := make([]*models.Ticket, 0, len(contacts))
	for _, c := range contacts {
		if c.Ticket() == nil {
			reopenable = append(reopenable, tickets[c.ID()])
		}
	}

	evts, err := models.ReopenTickets(ctx, rt, oa, userID, reopenable)
	if err != nil {
		return nil, nil, fmt.Errorf("error reopening tickets: %w", err)
	}

	skippedTickets := make(map[models.ContactID]*models.Ticket, len(skipped))
	for _, c := range skipped {
		skippedTickets[c] = tickets[c]
	}

	return evts, skippedTickets, nil

}
