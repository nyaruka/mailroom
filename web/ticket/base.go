package ticket

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
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
