package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
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
func handleAirtimeCreated(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.AirtimeCreated)

	// the post-commit lifecycle is callback-driven and the callback CAS requires external_id to match;
	// a row inserted with empty external_id can never be transitioned, so refuse to persist one. This
	// shouldn't happen with the current DT One service (Create always returns a provider id on success)
	// but it would silently strand a row if a future provider impl ever bypassed that.
	if event.ExternalID == "" {
		slog.Error("ignoring airtime_created event with empty external_id", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "event", event.UUID())
		return nil
	}

	slog.Debug("airtime created", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "sender", event.Sender, "recipient", event.Recipient, "currency", event.Currency, "amount", event.Amount.String())

	transfer := models.NewAirtimeTransfer(oa.OrgID(), scene.ContactID(), event)

	for _, httpLog := range event.HTTPLogs {
		transfer.AddLog(models.NewAirtimeTransferredLog(
			oa.OrgID(),
			httpLog.URL,
			httpLog.StatusCode,
			httpLog.Request,
			httpLog.Response,
			httpLog.Status != core.CallStatusSuccess,
			time.Duration(httpLog.ElapsedMS)*time.Millisecond,
			httpLog.Retries,
			httpLog.CreatedOn,
		))
	}

	scene.AttachPreCommitHook(hooks.InsertAirtimeTransfers, transfer)
	scene.AttachPostCommitHook(hooks.ConfirmAirtimeTransfers, transfer)

	return nil
}
