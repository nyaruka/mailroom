package ticket

import (
	"context"
	"fmt"
	"maps"
	"slices"

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
	Via         models.Via         `json:"via"          validate:"required,eq=api|eq=ui"`
}

type bulkTicketResponse struct {
	ChangedUUIDs []flows.TicketUUID                  `json:"changed_uuids"`
	Events       map[flows.ContactUUID][]flows.Event `json:"events"`
}

func newBulkResponse(eventsByContact map[*flows.Contact][]flows.Event) *bulkTicketResponse {
	changed := make([]flows.TicketUUID, 0, len(eventsByContact))
	eventMap := make(map[flows.ContactUUID][]flows.Event)

	for contact, evts := range eventsByContact {
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

		if len(evts) > 0 {
			eventMap[contact.UUID()] = append(eventMap[contact.UUID()], evts...)
		}
	}

	slices.Sort(changed)

	return &bulkTicketResponse{ChangedUUIDs: changed, Events: eventMap}
}

func modifyTickets(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, ticketUUIDs []flows.TicketUUID, mod func(*models.Ticket) flows.Modifier, via models.Via) (map[*flows.Contact][]flows.Event, error) {
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

	eventsByContact, _, err := runner.ModifyWithLock(ctx, rt, oa, userID, contactDs, modsByContact, byContact, via)
	if err != nil {
		return nil, fmt.Errorf("error applying ticket modifiers: %w", err)
	}

	return eventsByContact, nil
}
