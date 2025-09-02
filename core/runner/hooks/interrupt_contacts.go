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

// InterruptContacts is our hook for interrupting contacts
var InterruptContacts runner.PreCommitHook = &interruptContacts{}

type interruptContacts struct{}

func (h *interruptContacts) Order() int { return 0 } // run before everything else

func (h *interruptContacts) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// gather contacts by session status
	contacts := make(map[models.ContactID]flows.SessionStatus)
	for scene, args := range scenes {
		contacts[scene.DBContact.ID()] = args[0].(*runner.ContactInterruptedEvent).Status
	}

	if err := models.InterruptContacts(ctx, tx, contacts); err != nil {
		return fmt.Errorf("error interrupting contacts: %w", err)
	}

	return nil
}
