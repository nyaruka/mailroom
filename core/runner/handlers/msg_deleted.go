package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeMsgDeleted, handleMsgDeleted)
}

func handleMsgDeleted(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.MsgDeleted)

	slog.Debug("msg deleted", "contact", scene.ContactUUID(), "msg", event.MsgUUID)

	scene.AttachPreCommitHook(hooks.DeleteMessages, &hooks.MessageDeletion{
		MsgUUID:   event.MsgUUID,
		ByContact: event.ByContact,
		UserID:    userID,
	})
	scene.AttachPostCommitHook(hooks.DeindexMessages, event.MsgUUID)

	return nil
}
