package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeOptInRequested, handleOptInRequested)
}

func handleOptInRequested(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.OptInRequested)

	slog.Debug("optin requested", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), slog.Group("optin", "uuid", event.OptIn.UUID, "name", event.OptIn.Name))

	// get our opt in
	optIn := oa.OptInByUUID(event.OptIn.UUID)
	if optIn == nil {
		return fmt.Errorf("unable to load optin with uuid: %s", event.OptIn.UUID)
	}

	// get our channel
	channel := oa.ChannelByUUID(event.Channel.UUID)
	if channel == nil {
		return fmt.Errorf("unable to load channel with uuid: %s", event.Channel.UUID)
	}

	// and the flow
	flow := e.Step().Run().Flow().Asset().(*models.Flow)

	msg := models.NewOutgoingOptInMsg(rt, oa.OrgID(), scene.DBContact, flow, optIn, channel, event, scene.IncomingMsg)

	// register to have this message committed and sent
	scene.AttachPreCommitHook(hooks.InsertMessages, hooks.MsgAndURN{Msg: msg.Msg, URN: event.URN})
	scene.AttachPostCommitHook(hooks.SendMessages, msg)

	return nil
}
