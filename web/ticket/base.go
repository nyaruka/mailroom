package ticket

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tickets"
	"github.com/nyaruka/mailroom/runtime"
)

type bulkTicketRequest struct {
	OrgID     models.OrgID      `json:"org_id"      validate:"required"`
	UserID    models.UserID     `json:"user_id"      validate:"required"`
	TicketIDs []models.TicketID `json:"ticket_ids"`
}

type bulkTicketResponse struct {
	ChangedIDs []models.TicketID `json:"changed_ids"`
}

func newLegacyBulkResponse(changed map[*models.Ticket]*models.TicketEvent) *bulkTicketResponse {
	ids := make([]models.TicketID, 0, len(changed))
	for t := range changed {
		ids = append(ids, t.ID)
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return &bulkTicketResponse{ChangedIDs: ids}
}

func newBulkResponse(changed []*models.Ticket) *bulkTicketResponse {
	ids := make([]models.TicketID, len(changed))
	for i, t := range changed {
		ids[i] = t.ID
	}

	slices.Sort(ids)

	return &bulkTicketResponse{ChangedIDs: ids}
}

func createTicketScenes(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ticketIDs []models.TicketID) ([]*runner.Scene, error) {
	tickets, err := models.LoadTickets(ctx, rt.DB, ticketIDs)
	if err != nil {
		return nil, fmt.Errorf("error loading tickets: %w", err)
	}

	byContact := make(map[models.ContactID][]*models.Ticket, 10)
	for _, t := range tickets {
		byContact[t.ContactID] = append(byContact[t.ContactID], t)
	}

	scenes, err := runner.CreateScenes(ctx, rt, oa, slices.Collect(maps.Keys(byContact)))
	if err != nil {
		return nil, err
	}

	for _, s := range scenes {
		s.Tickets = byContact[s.ContactID()]
	}

	return scenes, nil
}

func ApplyTicketModifier(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, mod tickets.TicketModifier, userID models.UserID) (changed []*models.Ticket, err error) {
	for _, ticket := range scene.Tickets {
		evts := make([]flows.Event, 0)

		if mod(ticket, func(e flows.Event) { evts = append(evts, e) }) {
			changed = append(changed, ticket)
		}

		for _, evt := range evts {
			if err := scene.AddEvent(ctx, rt, oa, evt, userID); err != nil {
				return nil, fmt.Errorf("error adding event to scene: %w", err)
			}
		}
	}
	return changed, nil
}
