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
	"github.com/nyaruka/mailroom/runtime"
)

type bulkTicketRequest struct {
	OrgID     models.OrgID      `json:"org_id"      validate:"required"`
	UserID    models.UserID     `json:"user_id"      validate:"required"`
	TicketIDs []models.TicketID `json:"ticket_ids"`
}

type bulkTicketResponse struct {
	ChangedUUIDs []flows.TicketUUID `json:"changed_uuids,omitempty"`
	ChangedIDs   []models.TicketID  `json:"changed_ids,omitempty"` // deprecated
}

func newLegacyBulkResponse(changed map[*models.Ticket]*models.TicketEvent) *bulkTicketResponse {
	ids := make([]models.TicketID, 0, len(changed))
	for t := range changed {
		ids = append(ids, t.ID)
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return &bulkTicketResponse{ChangedIDs: ids}
}

func newBulkResponse(changed []flows.TicketUUID) *bulkTicketResponse {
	slices.Sort(changed)

	return &bulkTicketResponse{ChangedUUIDs: changed}
}

func createTicketScenes(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ticketIDs []models.TicketID) ([]*runner.Scene, error) {
	tickets, err := models.LoadTickets(ctx, rt.DB, ticketIDs)
	if err != nil {
		return nil, fmt.Errorf("error loading tickets: %w", err)
	}

	dbByContact := make(map[models.ContactID][]*models.Ticket, 10)
	byContact := make(map[models.ContactID][]*flows.Ticket, 10)

	for _, t := range tickets {
		dbByContact[t.ContactID] = append(dbByContact[t.ContactID], t)
		byContact[t.ContactID] = append(byContact[t.ContactID], t.EngineTicket(oa))
	}

	scenes, err := runner.CreateScenes(ctx, rt, oa, slices.Collect(maps.Keys(byContact)))
	if err != nil {
		return nil, err
	}

	for _, s := range scenes {
		s.DBTickets = dbByContact[s.ContactID()]
		s.Tickets = byContact[s.ContactID()]
	}

	return scenes, nil
}
