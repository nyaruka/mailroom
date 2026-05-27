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

// ConfirmAirtimeTransfers is our post-commit hook that triggers the provider to actually send the airtime for
// each pending transfer initiated during the sprint. Failures aren't retried: the transfer row stays pending so
// it's visible for manual triage. DT One auto-cancels unconfirmed transactions after its expiration window, so
// stuck-pending rows naturally transition to failed via the eventual status callback.
var ConfirmAirtimeTransfers runner.PostCommitHook = &confirmAirtimeTransfers{}

type confirmAirtimeTransfers struct{}

func (h *confirmAirtimeTransfers) Order() int { return 10 }

func (h *confirmAirtimeTransfers) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	httpClient := &http.Client{Timeout: 120 * time.Second}
	httpRetries := httpx.NewFixedRetries(time.Second*5, time.Second*10)

	for _, args := range scenes {
		for _, arg := range args {
			transfer := arg.(*models.AirtimeTransfer)

			if transfer.ExternalID() == "" {
				slog.Warn("airtime transfer has no provider id, cannot confirm", "transfer", transfer.UUID())
				continue
			}

			svc, err := oa.Org().AirtimeService(rt, httpClient, httpRetries)
			if err != nil {
				slog.Error("unable to get airtime service for transfer", "transfer", transfer.UUID(), "error", err)
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

			if confirmErr != nil {
				slog.Warn("airtime transfer failed to confirm, leaving pending", "transfer", transfer.UUID(), "error", confirmErr)
			}
		}
	}

	return nil
}
