package hooks

import (
	"context"
	"fmt"
	"maps"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

// InsertTickets is our hook for inserting tickets
var InsertTickets runner.PreCommitHook = &insertTickets{}

// TicketOpening is the item for the InsertTickets hook. Tallies is filled with the tally amounts which count the ticket
// being opened, and should also be attached to IncrementTallies so that they're only incremented if the scene commits.
// It's refilled rather than added to, as a failed bulk commit re-runs this hook for each scene.
type TicketOpening struct {
	Ticket  *models.Ticket
	Tallies map[string]int64
}

type insertTickets struct{}

func (h *insertTickets) Order() int { return 10 }

func (h *insertTickets) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our tickets
	tickets := make([]*models.Ticket, 0, len(scenes))
	openings := make([]*TicketOpening, 0, len(scenes))

	for _, args := range scenes {
		for _, a := range args {
			o := a.(*TicketOpening)
			tickets = append(tickets, o.Ticket)
			openings = append(openings, o)
		}
	}

	// insert the tickets
	if err := models.InsertTickets(ctx, tx, tickets); err != nil {
		return fmt.Errorf("error inserting tickets: %w", err)
	}

	for _, o := range openings {
		clear(o.Tallies)
		maps.Copy(o.Tallies, models.TicketOpenedTallies(oa, []*models.Ticket{o.Ticket}))
	}

	return nil
}
