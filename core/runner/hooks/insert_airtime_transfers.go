package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

// InsertAirtimeTransfers is our hook for inserting airtime transfers
var InsertAirtimeTransfers runner.PreCommitHook = &insertAirtimeTransfers{}

type insertAirtimeTransfers struct{}

func (h *insertAirtimeTransfers) Order() int { return 10 }

func (h *insertAirtimeTransfers) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our transfers
	transfers := make([]*models.AirtimeTransfer, 0, len(scenes))
	for _, args := range scenes {
		for _, t := range args {
			transfer := t.(*models.AirtimeTransfer)
			transfers = append(transfers, transfer)
		}
	}

	if err := models.InsertAirtimeTransfers(ctx, tx, transfers); err != nil {
		return fmt.Errorf("error inserting airtime transfers: %w", err)
	}

	return nil
}
