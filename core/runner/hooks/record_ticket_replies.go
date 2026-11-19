package hooks

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

// RecordTicketReplies is our hook for recording user replies to open tickets
var RecordTicketReplies runner.PreCommitHook = &recordTicketReplies{}

type recordTicketReplies struct{}

// TicketReply is the item for the RecordTicketReplies hook. Tallies is filled with the tally amounts which count the
// reply, and should also be attached to IncrementTallies so that they're only incremented if the scene commits. It's
// refilled rather than added to, as a failed bulk commit re-runs this hook for each scene.
type TicketReply struct {
	Ticket  *models.Ticket
	UserID  models.UserID
	When    time.Time
	Tallies map[string]int64
}

func (h *recordTicketReplies) Order() int { return 11 } // after InsertTickets so tickets opened in the same scene have ids

func (h *recordTicketReplies) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for _, args := range scenes {
		for _, a := range args {
			r := a.(TicketReply)

			tallies, err := models.RecordTicketReply(ctx, tx, oa, r.Ticket, r.UserID, r.When)
			if err != nil {
				return fmt.Errorf("error recording ticket reply: %w", err)
			}

			clear(r.Tallies)
			maps.Copy(r.Tallies, tallies)
		}
	}

	return nil
}
