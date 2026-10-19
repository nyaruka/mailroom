package hooks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

// RecordTicketReplies is our hook for recording user replies to open tickets
var RecordTicketReplies runner.PreCommitHook = &recordTicketReplies{}

type recordTicketReplies struct{}

type TicketReply struct {
	Ticket *models.Ticket
	UserID models.UserID
	When   time.Time
}

func (h *recordTicketReplies) Order() int { return 11 } // after InsertTickets so tickets opened in the same scene have ids

func (h *recordTicketReplies) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for _, args := range scenes {
		for _, a := range args {
			r := a.(TicketReply)

			if err := models.RecordTicketReply(ctx, tx, oa, r.Ticket, r.UserID, r.When); err != nil {
				return fmt.Errorf("error recording ticket reply: %w", err)
			}
		}
	}

	return nil
}
