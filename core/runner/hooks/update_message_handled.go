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

func (h *updateMessageHandled) Order() int { return 1 }

func (h *updateMessageHandled) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for scene, es := range scenes {
		evt := es[0].(*events.MsgReceivedEvent)
		msgIn := scene.IncomingMsg
		contactBlocked := scene.Contact().Status() == flows.ContactStatusBlocked

		var flow *models.Flow
		if scene.Session() != nil {
			flow, _ = scene.LocateEvent(evt)
		}

		visibility := models.VisibilityVisible
		if contactBlocked {
			visibility = models.VisibilityArchived
		}

		err := models.MarkMessageHandled(ctx, tx, msgIn.ID, models.MsgStatusHandled, visibility, flow, msgIn.Ticket, msgIn.Attachments, msgIn.LogUUIDs)
		if err != nil {
			return fmt.Errorf("error marking message as handled: %w", err)
		}

		if msgIn.Ticket != nil && !contactBlocked {
			err = models.UpdateTicketLastActivity(ctx, tx, []*models.Ticket{msgIn.Ticket})
			if err != nil {
				return fmt.Errorf("error updating last activity for ticket: %w", err)
			}
		}

	}

	return nil
}
