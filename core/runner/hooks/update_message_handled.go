package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// UpdateMessageHandled is our hook for updating incoming messages
var UpdateMessageHandled runner.PreCommitHook = &updateMessageHandled{}

type updateMessageHandled struct{}

func (h *updateMessageHandled) Order() int { return 90 }

func (h *updateMessageHandled) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for scene, args := range scenes {
		evt := args[0].(*events.MsgReceived)
		msgIn := scene.IncomingMsg
		contactBlocked := scene.Contact.Status() == flows.ContactStatusBlocked

		var flow *models.Flow
		if scene.Sprint != nil && len(scene.Sprint.Flows()) > 0 {
			flow = scene.Sprint.Flows()[0].Asset().(*models.Flow)
		}

		visibility := models.VisibilityVisible
		if contactBlocked || evt.Msg.Channel() == nil {
			visibility = models.VisibilityArchived
		}

		// associate this message with the last open ticket for this contact
		var ticket *models.Ticket
		if tks := scene.DBContact.OpenTickets(); len(tks) > 0 {
			ticket = tks[len(tks)-1]
		}

		err := models.MarkMessageHandled(ctx, tx, msgIn.ID, models.MsgStatusHandled, visibility, flow, ticket, msgIn.Attachments, msgIn.LogUUIDs)
		if err != nil {
			return fmt.Errorf("error marking message as handled: %w", err)
		}

		if ticket != nil && !contactBlocked {
			if err := models.UpdateTicketLastActivity(ctx, tx, []*models.Ticket{ticket}); err != nil {
				return fmt.Errorf("error updating last activity for ticket: %w", err)
			}
		}

	}

	return nil
}
