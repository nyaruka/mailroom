package hooks

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/core"
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

var confirmHTTPRetries = httpx.NewFixedRetries(time.Second*5, time.Second*10)

func (h *confirmAirtimeTransfers) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// queued here rather than pre-commit so we don't write logs for transfers that never got committed
	for _, args := range scenes {
		for _, arg := range args {
			queueAirtimeLog(rt, arg.(*models.AirtimeTransfer).CreateLog)
		}
	}

	// hoist the airtime service out of the per-transfer loop — the org is constant for this hook invocation
	svc, err := oa.Org().AirtimeService(rt, rt.HTTP.Services, confirmHTTPRetries)
	if err != nil {
		// every transfer here belongs to this org, so we can't make progress on any of them. Log per
		// transfer rather than a single summary line so the affected UUIDs are searchable / countable
		// in aggregated logs — a misconfigured org silently dropping airtime would otherwise only show
		// up as one ERROR per sprint regardless of how many transfers were affected.
		for _, args := range scenes {
			for _, arg := range args {
				transfer := arg.(*models.AirtimeTransfer)
				slog.Error("unable to get airtime service, leaving transfer in Created", "transfer", transfer.UUID(), "org_id", oa.OrgID(), "error", err)
			}
		}
		return nil
	}

	for _, args := range scenes {
		for _, arg := range args {
			transfer := arg.(*models.AirtimeTransfer)

			logger := &core.HTTPLogger{}
			confirmErr := svc.Confirm(ctx, &core.AirtimeTransfer{ExternalID: transfer.ExternalID()}, logger.Log)

			queueAirtimeLog(rt, models.NewAirtimeLog(models.AirtimeLogTypeConfirm, oa.OrgID(), transfer.UUID(), logger.Logs, confirmErr))

			if confirmErr != nil {
				slog.Warn("airtime transfer failed to confirm, leaving pending", "transfer", transfer.UUID(), "error", confirmErr)
			}
		}
	}

	return nil
}

// queueAirtimeLog queues the given log to DynamoDB if it recorded anything. Failure to log doesn't fail the transfer.
func queueAirtimeLog(rt *runtime.Runtime, l *models.AirtimeLog) {
	if l == nil || (len(l.HttpLogs) == 0 && len(l.Errors) == 0) {
		return
	}
	if _, err := rt.Dynamo.Main.Queue(l); err != nil {
		slog.Error("error queuing airtime log", "transfer", l.DynamoKey().PK, "error", err)
	}
}
