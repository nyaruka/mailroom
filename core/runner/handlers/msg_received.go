package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeMsgReceived, handleMsgReceived)
}

// handleMsgReceived takes care of update last seen on and any campaigns based on that
func handleMsgReceived(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.MsgReceived)

	slog.Debug("msg received", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "text", event.Msg.Text(), "urn", event.Msg.URN())

	// update the message to be handled
	if scene.IncomingMsg != nil && !scene.IncomingMsg.Handled {
		scene.AttachPreCommitHook(hooks.UpdateMessageHandled, event)

		// which also counts as activity on the ticket unless the contact is blocked
		if event.TicketUUID != "" && scene.Contact.Status() != core.ContactStatusBlocked {
			if ticket := scene.DBContact.FindTicket(event.TicketUUID); ticket != nil && ticket.Status == models.TicketStatusOpen {
				scene.AttachPreCommitHook(hooks.UpdateTicketFires, ticket)
			}
		}
	}

	// index message to Elasticsearch if it has sufficient text
	if len(event.Msg.Text()) >= search.MessageTextMinLength {
		scene.AttachPostCommitHook(hooks.IndexMessages, &search.MessageDoc{
			CreatedOn:   event.CreatedOn(),
			OrgID:       oa.OrgID(),
			UUID:        event.UUID(),
			ContactUUID: scene.ContactUUID(),
			URNPath:     event.Msg.URN().Path(),
			Text:        event.Msg.Text(),
			InTicket:    event.TicketUUID != "",
		})
	}

	return nil
}
