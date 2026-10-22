package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

// UpdateTicketFires is our hook for scheduling the automatic closing of tickets which have had activity, or for
// unscheduling it for tickets which have been closed
var UpdateTicketFires runner.PreCommitHook = &updateTicketFires{}

type updateTicketFires struct{}

func (h *updateTicketFires) Order() int { return 10 }

func (h *updateTicketFires) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// a ticket can be attached more than once in a scene, e.g. opened then closed, so use its final status
	seen := make(map[*models.Ticket]bool)
	open := make([]*models.Ticket, 0, len(scenes))
	closed := make([]*models.Ticket, 0, len(scenes))

	for _, args := range scenes {
		for _, a := range args {
			t := a.(*models.Ticket)
			if seen[t] {
				continue
			}
			seen[t] = true

			if t.Status == models.TicketStatusOpen {
				open = append(open, t)
			} else {
				closed = append(closed, t)
			}
		}
	}

	if idleDays := oa.Org().MaxTicketIdleDays(); idleDays > 0 && len(open) > 0 {
		if err := models.UpsertTicketCloseFires(ctx, tx, idleDays, open); err != nil {
			return fmt.Errorf("error upserting ticket close fires: %w", err)
		}
	}

	if len(closed) > 0 {
		if err := models.DeleteTicketCloseFires(ctx, tx, closed); err != nil {
			return fmt.Errorf("error deleting ticket close fires: %w", err)
		}
	}

	return nil
}
