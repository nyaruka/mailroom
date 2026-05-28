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
// for each pending transfer initiated during the sprint. Confirm failures are not retried and leave the
// row pending; DT One auto-cancels held transactions after its expiration window and sends us a callback
// that transitions the row to failed, so even the "transaction permanently stuck" case converges.
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
