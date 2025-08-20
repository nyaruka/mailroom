package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InsertAirtimeTransfers is our hook for inserting airtime transfers
var InsertAirtimeTransfers runner.PreCommitHook = &insertAirtimeTransfers{}

type insertAirtimeTransfers struct{}

func (h *insertAirtimeTransfers) Order() int { return 10 }

func (h *insertAirtimeTransfers) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our transfers
	transfers := make([]*models.AirtimeTransfer, 0, len(scenes))
	for _, ts := range scenes {
		for _, t := range ts {
			transfer := t.(*models.AirtimeTransfer)
			transfers = append(transfers, transfer)
		}
	}

	if err := models.InsertAirtimeTransfers(ctx, tx, transfers); err != nil {
		return fmt.Errorf("error inserting airtime transfers: %w", err)
	}

	// gather all our logs and set the newly inserted transfer IDs on them
	logs := make([]*models.HTTPLog, 0, len(scenes))
	for _, t := range transfers {
		for _, l := range t.Logs {
			l.SetAirtimeTransferID(t.ID())
			logs = append(logs, l)
		}
	}

	if err := models.InsertHTTPLogs(ctx, tx, logs); err != nil {
		return fmt.Errorf("error inserting airtime transfer logs: %w", err)
	}

	return nil
}
