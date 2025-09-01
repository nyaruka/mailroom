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

var UpdateTicketTopic runner.PreCommitHook = &updateTicketTopic{}

type updateTicketTopic struct{}

func (h *updateTicketTopic) Order() int { return 10 }

func (h *updateTicketTopic) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	byChange := make(map[topicAndUser][]models.TicketID)
	for _, es := range scenes {
		for _, e := range es {
			u := e.(TicketTopicUpdate)
			byChange[topicAndUser{u.Topic, u.UserID}] = append(byChange[topicAndUser{u.Topic, u.UserID}], u.Ticket.ID())
		}
	}

	for topicAndUser, ticketIDs := range byChange {
		if err := models.TicketsChangeTopic(ctx, tx, oa, topicAndUser.userID, ticketIDs, topicAndUser.topic.ID()); err != nil {
			return fmt.Errorf("error changing ticket topics: %w", err)
		}
	}

	return nil
}

type topicAndUser struct {
	topic  *models.Topic
	userID models.UserID
}
