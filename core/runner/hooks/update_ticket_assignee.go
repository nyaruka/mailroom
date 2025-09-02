package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

type TicketAssigneeUpdate struct {
	Ticket     *models.Ticket
	AssigneeID models.UserID
	UserID     models.UserID
}

var UpdateTicketAssignee runner.PreCommitHook = &updateTicketAssignee{}

type updateTicketAssignee struct{}

func (h *updateTicketAssignee) Order() int { return 10 }

func (h *updateTicketAssignee) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	byChange := make(map[assigneeAndUser][]*models.Ticket)
	for _, es := range scenes {
		for _, e := range es {
			u := e.(TicketAssigneeUpdate)
			byChange[assigneeAndUser{u.AssigneeID, u.UserID}] = append(byChange[assigneeAndUser{u.AssigneeID, u.UserID}], u.Ticket)
		}
	}

	for assigneeAndUser, tickets := range byChange {
		if err := models.TicketsChangeAssignee(ctx, tx, oa, assigneeAndUser.userID, tickets, assigneeAndUser.assigneeID); err != nil {
			return fmt.Errorf("error changing ticket topics: %w", err)
		}
	}

	return nil
}

type assigneeAndUser struct {
	assigneeID models.UserID
	userID     models.UserID
}
