package tickets

import (
	"fmt"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
)

type TicketModifier func(ticket *models.Ticket, log flows.EventCallback) bool

func NewAssignmentModifier(oa *models.OrgAssets, assigneeID models.UserID) TicketModifier {
	newAssignee := oa.UserByID(assigneeID)

	return func(ticket *models.Ticket, log flows.EventCallback) bool {
		if ticket.AssigneeID != assigneeID {
			now := dates.Now()
			prevAssignee := oa.UserByID(ticket.AssigneeID)

			ticket.AssigneeID = assigneeID
			ticket.ModifiedOn = now

			log(events.NewTicketAssigneeChanged(ticket.UUID, newAssignee.Reference(), prevAssignee.Reference()))
			return true
		}
		return false
	}
}

func NewNoteModifier(oa *models.OrgAssets, note string) TicketModifier {
	return func(ticket *models.Ticket, log flows.EventCallback) bool {
		now := dates.Now()

		ticket.ModifiedOn = now
		ticket.LastActivityOn = now

		log(events.NewTicketNoteAdded(ticket.UUID, note))
		return true
	}
}

func NewTopicModifier(oa *models.OrgAssets, topicID models.TopicID) (TicketModifier, error) {
	topic := oa.TopicByID(topicID)
	if topic == nil {
		return nil, fmt.Errorf("no such topic with id: %d", topicID)
	}

	return func(ticket *models.Ticket, log flows.EventCallback) bool {
		if ticket.TopicID != topicID {
			now := dates.Now()

			ticket.TopicID = topicID
			ticket.ModifiedOn = now

			log(events.NewTicketTopicChanged(ticket.UUID, topic.Reference()))
			return true
		}
		return false
	}, nil
}
