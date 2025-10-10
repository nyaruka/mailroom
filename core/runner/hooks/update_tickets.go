package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

var UpdateTickets runner.PreCommitHook = &updateTickets{}

type updateTickets struct{}

func (h *updateTickets) Order() int { return 10 }

func (h *updateTickets) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	tickets := make([]*models.Ticket, 0, len(scenes))

	for _, args := range scenes {
		for _, item := range args {
			tickets = append(tickets, item.(*models.Ticket))
		}
	}

	if err := models.UpdateTickets(ctx, tx, tickets); err != nil {
		return fmt.Errorf("error updating tickets: %w", err)
	}

	return nil
}
