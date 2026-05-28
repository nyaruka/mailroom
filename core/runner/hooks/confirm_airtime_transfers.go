package hooks

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// ConfirmAirtimeTransfers is our post-commit hook that triggers the provider to actually send the airtime
// for each pending transfer initiated during the sprint.
//
// Confirm failures are *not* retried: rows that hit a permanent error (4xx from the provider) are flipped
// to failed immediately so the contact's flow can react; transient errors (5xx, network) leave the row
// pending and rely on the provider's eventual status callback (DT One auto-cancels unconfirmed transactions
// after its expiration window).
var ConfirmAirtimeTransfers runner.PostCommitHook = &confirmAirtimeTransfers{}

type confirmAirtimeTransfers struct{}

func (h *confirmAirtimeTransfers) Order() int { return 10 }

// shared across Execute invocations so the underlying connection pool is reused across hooks rather than
// dropped each time. The long timeout covers the worst-case Confirm round-trip with the configured retries.
var confirmHTTPClient = &http.Client{Timeout: 120 * time.Second}
var confirmHTTPRetries = httpx.NewFixedRetries(time.Second*5, time.Second*10)

func (h *confirmAirtimeTransfers) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// hoist the airtime service out of the per-transfer loop — the org is constant for this hook invocation
	svc, err := oa.Org().AirtimeService(rt, confirmHTTPClient, confirmHTTPRetries)
	if err != nil {
		// every transfer here belongs to this org, so we can't make progress on any of them
		slog.Error("unable to get airtime service, leaving transfers pending", "org_id", oa.OrgID(), "error", err)
		return nil
	}

	for _, args := range scenes {
		for _, arg := range args {
			transfer := arg.(*models.AirtimeTransfer)

			if transfer.ExternalID() == "" {
				// no provider id means Create returned nothing to confirm — mark failed rather than leaving stuck
				slog.Warn("airtime transfer has no provider id, marking failed", "transfer", transfer.UUID())
				h.markFailed(ctx, rt, transfer)
				continue
			}

			logger := &flows.HTTPLogger{}
			confirmErr := svc.Confirm(ctx, &flows.AirtimeTransfer{ExternalID: transfer.ExternalID()}, logger.Log)

			for _, l := range logger.Logs {
				log := models.NewAirtimeTransferredLog(
					oa.OrgID(),
					l.URL,
					l.StatusCode,
					l.Request,
					l.Response,
					l.Status != flows.CallStatusSuccess,
					time.Duration(l.ElapsedMS)*time.Millisecond,
					l.Retries,
					l.CreatedOn,
				)
				log.SetAirtimeTransferID(transfer.ID())
				if err := models.InsertHTTPLogs(ctx, rt.DB, []*models.HTTPLog{log}); err != nil {
					slog.Error("error inserting airtime transfer http log", "transfer", transfer.UUID(), "error", err)
				}
			}

			if confirmErr == nil {
				continue
			}

			// permanent failures (4xx, malformed id) → flip to failed so the flow doesn't hang on a zombie row.
			// transient failures (5xx, connection error) leave the row pending — the provider will eventually
			// auto-cancel the held transaction and send a callback that transitions us to failed.
			if isPermanentConfirmError(logger.Logs, confirmErr) {
				slog.Warn("airtime transfer failed to confirm permanently, marking failed", "transfer", transfer.UUID(), "error", confirmErr)
				h.markFailed(ctx, rt, transfer)
			} else {
				slog.Warn("airtime transfer failed to confirm transiently, leaving pending", "transfer", transfer.UUID(), "error", confirmErr)
			}
		}
	}

	return nil
}

func (h *confirmAirtimeTransfers) markFailed(ctx context.Context, rt *runtime.Runtime, transfer *models.AirtimeTransfer) {
	if _, err := models.UpdateAirtimeTransferStatus(ctx, rt.DB, transfer.UUID(), models.AirtimeTransferStatusFailed); err != nil {
		slog.Error("error marking airtime transfer as failed", "transfer", transfer.UUID(), "error", err)
	}
}

// isPermanentConfirmError decides whether a Confirm error is worth giving up on immediately. If we got an
// HTTP response back at all and it was a 4xx, the provider rejected the confirm (id unknown, already
// confirmed, etc.) and retrying won't help. Anything else (no HTTP log, 5xx, connection error) is treated
// as transient.
func isPermanentConfirmError(logs []*flows.HTTPLog, err error) bool {
	if len(logs) == 0 {
		return true // didn't even reach the provider — most likely a malformed external_id
	}
	last := logs[len(logs)-1]
	return last.StatusCode >= 400 && last.StatusCode < 500
}
