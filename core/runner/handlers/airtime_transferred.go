package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/shopspring/decimal"
)

func init() {
	runner.RegisterEventHandler(events.TypeAirtimeTransferred, handleAirtimeTransferred)
}

// handleAirtimeTransferred is called for each airtime transferred event
func handleAirtimeTransferred(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.AirtimeTransferred)

	slog.Debug("airtime transferred", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "sender", event.Sender, "recipient", event.Recipient, "currency", event.Currency, "amount", event.Amount.String())

	status := models.AirtimeTransferStatusSuccess
	if event.Amount == decimal.Zero {
		status = models.AirtimeTransferStatusFailed
	}

	transfer := models.NewAirtimeTransfer(
		event.TransferUUID,
		oa.OrgID(),
		status,
		event.ExternalID,
		scene.ContactID(),
		event.Sender,
		event.Recipient,
		event.Currency,
		event.Amount,
		event.CreatedOn(),
	)

	// add a log for each HTTP call
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

	return nil
}
