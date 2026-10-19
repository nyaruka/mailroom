package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// TypeCloseInactiveTickets is the type of the close inactive tickets task
const TypeCloseInactiveTickets = "close_inactive_tickets"

func init() {
	RegisterType(TypeCloseInactiveTickets, func() Task { return &CloseInactiveTickets{} })
}

// CloseInactiveTickets is our task for closing the open tickets of contacts which have had no activity since a given
// time. Closes aren't credited to any user.
type CloseInactiveTickets struct {
	ContactIDs []models.ContactID `json:"contact_ids" validate:"required"`
	OlderThan  time.Time          `json:"older_than"  validate:"required"`
}

func (t *CloseInactiveTickets) Type() string {
	return TypeCloseInactiveTickets
}

// Timeout is the maximum amount of time the task can run for
func (t *CloseInactiveTickets) Timeout() time.Duration {
	return 10 * time.Minute
}

func (t *CloseInactiveTickets) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *CloseInactiveTickets) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error {
	// contacts we can't lock are left for the next run of the cron which queued this
	scenes, _, unlock, err := runner.LockAndLoad(ctx, rt, oa, t.ContactIDs, nil, 10*time.Second)
	if err != nil {
		return fmt.Errorf("error locking contacts: %w", err)
	}
	defer unlock()

	for _, scene := range scenes {
		// check activity against the freshly loaded tickets, as a ticket may have had activity since it was queued
		for _, ticket := range scene.DBContact.Tickets() {
			if ticket.LastActivityOn.Before(t.OlderThan) {
				if err := scene.ApplyModifier(ctx, rt, oa, modifiers.NewTicketClose(ticket.UUID), models.NilUserID, ""); err != nil {
					return fmt.Errorf("error closing ticket %s: %w", ticket.UUID, err)
				}
			}
		}
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return fmt.Errorf("error committing scenes: %w", err)
	}

	return nil
}
