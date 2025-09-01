package hooks

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

var InsertLegacyTicketEvents runner.PreCommitHook = &insertLegacyTicketEvents{}

type insertLegacyTicketEvents struct{}

func (h *insertLegacyTicketEvents) Order() int { return 10 }

func (h *insertLegacyTicketEvents) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	events := make([]*models.TicketEvent, 0, len(scenes))

	for _, es := range scenes {
		for _, e := range es {
			events = append(events, e.(*models.TicketEvent))
		}
	}

	if err := models.InsertLegacyTicketEvents(ctx, tx, events); err != nil {
		return err
	}

	return nil
}
