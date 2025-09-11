package ticket

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

type bulkTicketRequest struct {
	OrgID       models.OrgID       `json:"org_id"       validate:"required"`
	UserID      models.UserID      `json:"user_id"      validate:"required"`
	TicketUUIDs []flows.TicketUUID `json:"ticket_uuids" validate:"required"`
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

func modifyTickets(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, ticketUUIDs []flows.TicketUUID, mod func(*models.Ticket) flows.Modifier) ([]flows.TicketUUID, error) {
	tickets, err := models.LoadTickets(ctx, rt.DB, oa.OrgID(), ticketUUIDs)
	if err != nil {
		return nil, fmt.Errorf("error loading tickets: %w", err)
	}

	byContact := make(map[models.ContactID][]*models.Ticket, len(ticketUUIDs))
	modsByContact := make(map[models.ContactID][]flows.Modifier, len(ticketUUIDs))

	for _, ticket := range tickets {
		byContact[ticket.ContactID] = append(byContact[ticket.ContactID], ticket)
		modsByContact[ticket.ContactID] = append(modsByContact[ticket.ContactID], mod(ticket))
	}

	contactDs := slices.Collect(maps.Keys(byContact))

	eventsByContact, _, err := runner.ModifyWithLock(ctx, rt, oa, userID, contactDs, modsByContact, byContact)
	if err != nil {
		return nil, fmt.Errorf("error applying ticket modifiers: %w", err)
	}

	// get changed tickets from events
	changed := make([]flows.TicketUUID, 0, len(ticketUUIDs))
	for _, evts := range eventsByContact {
		for _, e := range evts {
			switch typed := e.(type) {
			case *events.TicketAssigneeChanged:
				changed = append(changed, typed.TicketUUID)
			case *events.TicketClosed:
				changed = append(changed, typed.TicketUUID)
			case *events.TicketNoteAdded:
				changed = append(changed, typed.TicketUUID)
			case *events.TicketReopened:
				changed = append(changed, typed.TicketUUID)
			case *events.TicketTopicChanged:
				changed = append(changed, typed.TicketUUID)
			}
		}
	}

	return changed, nil
}
