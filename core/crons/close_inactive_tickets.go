package crons

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	Register("close_inactive_tickets", &CloseInactiveTicketsCron{MaxTickets: 10_000, TaskBatchSize: 100})
}

// CloseInactiveTicketsCron finds open tickets which have gone without activity for longer than the configured maximum
// and queues tasks to close them.
type CloseInactiveTicketsCron struct {
	// limits each run to the oldest tickets so that a large backlog drains over several runs rather than being
	// re-queued in full every run while the tasks from previous runs are still waiting
	MaxTickets    int
	TaskBatchSize int
}

func (c *CloseInactiveTicketsCron) Next(last time.Time) time.Time {
	return Next(last, time.Hour)
}

func (c *CloseInactiveTicketsCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	if rt.Config.TicketMaxIdleDays <= 0 {
		return map[string]any{"tickets": 0}, nil
	}

	olderThan := dates.Now().Add(-time.Duration(rt.Config.TicketMaxIdleDays) * 24 * time.Hour)

	tickets, err := models.GetInactiveTickets(ctx, rt.DB, olderThan, c.MaxTickets)
	if err != nil {
		return nil, err
	}

	contactsByOrg := make(map[models.OrgID][]models.ContactID)
	seen := make(map[models.ContactID]bool, len(tickets))
	for _, t := range tickets {
		if !seen[t.ContactID] {
			contactsByOrg[t.OrgID] = append(contactsByOrg[t.OrgID], t.ContactID)
			seen[t.ContactID] = true
		}
	}

	for orgID, contactIDs := range contactsByOrg {
		for batch := range slices.Chunk(contactIDs, c.TaskBatchSize) {
			task := &tasks.CloseInactiveTickets{ContactIDs: batch, OlderThan: olderThan}
			if err := tasks.Queue(ctx, rt, rt.Queues.Batch, orgID, task, false); err != nil {
				return nil, fmt.Errorf("error queuing close inactive tickets task for org #%d: %w", orgID, err)
			}
		}
	}

	return map[string]any{"tickets": len(tickets)}, nil
}
