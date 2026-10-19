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
	Register("close_inactive_tickets", &CloseInactiveTicketsCron{FetchBatchSize: 1_000, TaskBatchSize: 100})
}

// CloseInactiveTicketsCron finds open tickets which have gone without activity for longer than the configured maximum
// and queues tasks to close them.
type CloseInactiveTicketsCron struct {
	FetchBatchSize int
	TaskBatchSize  int
}

func (c *CloseInactiveTicketsCron) Next(last time.Time) time.Time {
	return Next(last, time.Hour)
}

func (c *CloseInactiveTicketsCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	if rt.Config.TicketMaxIdleDays <= 0 {
		return map[string]any{"tickets": 0}, nil
	}

	olderThan := dates.Now().Add(-time.Duration(rt.Config.TicketMaxIdleDays) * 24 * time.Hour)
	numTickets := 0

	var afterOn time.Time
	var afterID models.TicketID

	for {
		tickets, err := models.GetInactiveTickets(ctx, rt.DB, olderThan, afterOn, afterID, c.FetchBatchSize)
		if err != nil {
			return nil, err
		}

		contactsByOrg := make(map[models.OrgID][]models.ContactID)
		for _, t := range tickets {
			if !slices.Contains(contactsByOrg[t.OrgID], t.ContactID) {
				contactsByOrg[t.OrgID] = append(contactsByOrg[t.OrgID], t.ContactID)
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

		numTickets += len(tickets)

		if len(tickets) < c.FetchBatchSize {
			break
		}

		last := tickets[len(tickets)-1]
		afterOn, afterID = last.LastActivityOn, last.ID
	}

	return map[string]any{"tickets": numTickets}, nil
}
