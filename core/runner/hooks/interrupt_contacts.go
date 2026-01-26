package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// InterruptContacts is our hook for interrupting contacts
var InterruptContacts runner.PreCommitHook = &interruptContacts{}

type interruptContacts struct{}

func (h *interruptContacts) Order() int { return 0 } // run before everything else

func (h *interruptContacts) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// group by status
	byStatus := make(map[flows.SessionStatus][]*models.Contact)
	for scene, args := range scenes {
		e := args[0].(*runner.ContactInterruptedEvent)

		byStatus[e.Status] = append(byStatus[e.Status], scene.DBContact)
	}

	for status, contacts := range byStatus {
		if err := models.InterruptContacts(ctx, tx, contacts, status); err != nil {
			return fmt.Errorf("error interrupting contacts: %w", err)
		}
	}

	return nil
}
