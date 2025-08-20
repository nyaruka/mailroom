package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

type DeleteFires string

const (
	DeleteFiresNone  DeleteFires = "none"
	DeleteFiresWaits DeleteFires = "waits"
	DeleteFiresAll   DeleteFires = "all"
)

type FiresSet struct {
	Create []*models.ContactFire
	Delete DeleteFires
}

// InsertContactFires is our hook for inserting contact fires
var InsertContactFires runner.PreCommitHook = &insertContactFires{}

type insertContactFires struct{}

func (h *insertContactFires) Order() int { return 10 }

func (h *insertContactFires) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather all our fires
	create := make([]*models.ContactFire, 0, len(scenes))
	deleteAll := make([]models.ContactID, 0, len(scenes))
	deleteWaits := make([]models.ContactID, 0, len(scenes))

	for scene, args := range scenes {
		for _, fs := range args {
			sc, sd := fs.(FiresSet).Create, fs.(FiresSet).Delete

			create = append(create, sc...)

			if sd == DeleteFiresAll {
				deleteAll = append(deleteAll, scene.ContactID())
			} else if sd == DeleteFiresWaits {
				deleteWaits = append(deleteWaits, scene.ContactID())
			}
		}
	}

	if len(deleteAll) > 0 {
		_, err := models.DeleteSessionFires(ctx, tx, deleteAll, true)
		if err != nil {
			return fmt.Errorf("error deleting all session contact fires: %w", err)
		}
	}
	if len(deleteWaits) > 0 {
		_, err := models.DeleteSessionFires(ctx, tx, deleteWaits, false)
		if err != nil {
			return fmt.Errorf("error deleting wait session contact fires: %w", err)
		}
	}

	if err := models.InsertContactFires(ctx, tx, create); err != nil {
		return fmt.Errorf("error inserting contact fires: %w", err)
	}

	return nil
}
