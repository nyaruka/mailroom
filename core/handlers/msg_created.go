package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/hooks"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	models.RegisterEventHandler(events.TypeMsgCreated, handleMsgCreated)
}

// handleMsgCreated creates the db msg for the passed in event
func handleMsgCreated(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scene *models.Scene, e flows.Event) error {
	event := e.(*events.MsgCreatedEvent)

	// must be in a session
	if scene.Session() == nil {
		return fmt.Errorf("cannot handle msg created event without session")
	}

	slog.Debug("msg created", "contact", scene.ContactUUID(), "session", scene.SessionID(), "text", event.Msg.Text(), "urn", event.Msg.URN())

	// messages in messaging flows must have urn id set on them, if not, go look it up
	if scene.Session().SessionType() == models.FlowTypeMessaging && event.Msg.URN() != urns.NilURN {
		urn := event.Msg.URN()
		if models.GetURNInt(urn, "id") == 0 {
			urn, err := models.GetOrCreateURN(ctx, tx, oa, scene.ContactID(), event.Msg.URN())
			if err != nil {
				return fmt.Errorf("unable to get or create URN: %s: %w", event.Msg.URN(), err)
			}
			// update our Msg with our full URN
			event.Msg.SetURN(urn)
		}
	}

	// get our channel
	var channel *models.Channel
	if event.Msg.Channel() != nil {
		channel = oa.ChannelByUUID(event.Msg.Channel().UUID)
		if channel == nil {
			return fmt.Errorf("unable to load channel with uuid: %s", event.Msg.Channel().UUID)
		}
	}

	// and the flow
	flow, _ := scene.Session().LocateEvent(e)

	msg, err := models.NewOutgoingFlowMsg(rt, oa.Org(), channel, scene.Session(), flow, event.Msg, event.CreatedOn())
	if err != nil {
		return fmt.Errorf("error creating outgoing message to %s: %w", event.Msg.URN(), err)
	}

	// commit this message in the transaction
	scene.AppendToEventPreCommitHook(hooks.CommitMessagesHook, msg)

	// and queue it to be sent after the transaction is complete
	scene.AppendToEventPostCommitHook(hooks.SendMessagesHook, msg)

	return nil
}
