package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeAirtimeCreated, handleAirtimeCreated)
}

// handleAirtimeCreated is called for each airtime created event. It persists the transfer in pending state
// during pre-commit, then schedules the actual provider call as a post-commit hook so the row exists by
// the time provider callbacks could arrive.
func handleAirtimeCreated(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.AirtimeCreated)

	slog.Debug("airtime created", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "sender", event.Sender, "recipient", event.Recipient, "currency", event.Currency, "amount", event.Amount.String())

	transfer := models.NewAirtimeTransfer(oa.OrgID(), scene.ContactID(), event)

	for _, httpLog := range event.HTTPLogs {
		transfer.AddLog(models.NewAirtimeTransferredLog(
			oa.OrgID(),
			httpLog.URL,
			httpLog.StatusCode,
			httpLog.Request,
			httpLog.Response,
			httpLog.Status != flows.CallStatusSuccess,
			time.Duration(httpLog.ElapsedMS)*time.Millisecond,
			httpLog.Retries,
			httpLog.CreatedOn,
		))
	}

	scene.AttachPreCommitHook(hooks.InsertAirtimeTransfers, transfer)
	scene.AttachPostCommitHook(hooks.ConfirmAirtimeTransfers, transfer)

	return nil
}
