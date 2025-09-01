package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

type TicketTopicUpdate struct {
	Ticket *models.Ticket
	Topic  *models.Topic
	UserID models.UserID
}

var UpdateTicketTopics runner.PreCommitHook = &updateTicketTopics{}

type updateTicketTopics struct{}

func (h *updateTicketTopics) Order() int { return 10 }

func (h *updateTicketTopics) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	byTopicAndUser := make(map[topicAndUser][]models.TicketID)
	for _, es := range scenes {
		for _, e := range es {
			u := e.(TicketTopicUpdate)
			byTopicAndUser[topicAndUser{u.Topic, u.UserID}] = append(byTopicAndUser[topicAndUser{u.Topic, u.UserID}], u.Ticket.ID())
		}
	}

	for topicAndUser, ticketUUIDs := range byTopicAndUser {
		if err := models.TicketsChangeTopic(ctx, tx, oa, topicAndUser.userID, ticketUUIDs, topicAndUser.topic.ID()); err != nil {
			return fmt.Errorf("error changing ticket topics: %w", err)
		}
	}

	return nil
}

type topicAndUser struct {
	topic  *models.Topic
	userID models.UserID
}
