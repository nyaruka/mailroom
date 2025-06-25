package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InsertSessions is our hook for interrupting existing sessions
var InsertSessions runner.PreCommitHook = &insertSessions{}

type insertSessions struct{}

func (h *insertSessions) Order() int { return 0 } // run before everything else

func (h *insertSessions) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	interruptIDs := make([]models.ContactID, 0, len(scenes))
	for s := range scenes {
		if s.Interrupt {
			interruptIDs = append(interruptIDs, s.ContactID())
		}
	}
	if len(interruptIDs) > 0 {
		if err := models.InterruptSessionsForContactsTx(ctx, tx, interruptIDs); err != nil {
			return fmt.Errorf("error interrupting contacts: %w", err)
		}
	}

	sessions := make([]flows.Session, 0, len(scenes))
	sprints := make([]flows.Sprint, 0, len(scenes))
	contacts := make([]*models.Contact, 0, len(scenes))
	callIDs := make([]models.CallID, 0, len(scenes))
	startIDs := make([]models.StartID, 0, len(scenes))
	for s := range scenes {
		sessions = append(sessions, s.Session)
		sprints = append(sprints, s.Sprint)
		contacts = append(contacts, s.DBContact)
		if s.DBCall != nil {
			callIDs = append(callIDs, s.DBCall.ID())
		} else {
			callIDs = append(callIDs, models.NilCallID)
		}
		startIDs = append(startIDs, s.StartID)
	}

	_, err := models.InsertSessions(ctx, rt, tx, oa, sessions, sprints, contacts, callIDs, startIDs)
	if err != nil {
		return fmt.Errorf("error inserting sessions: %w", err)
	}

	return nil
}
