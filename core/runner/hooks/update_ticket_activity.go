package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

var UpdateTicketActivity runner.PreCommitHook = &updateTicketActivity{}

type updateTicketActivity struct{}

func (h *updateTicketActivity) Order() int { return 10 }

func (h *updateTicketActivity) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	tickets := make([]*models.Ticket, 0, len(scenes))
	for _, args := range scenes {
		for _, e := range args {
			tickets = append(tickets, e.(*models.Ticket))
		}
	}

	if err := models.UpdateTicketLastActivity(ctx, tx, tickets); err != nil {
		return fmt.Errorf("error updating ticket last activity: %w", err)
	}

	return nil
}
