package hooks

import (
	"context"
	"fmt"
	"maps"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/vinovest/sqlx"
)

// InterruptContacts is our hook for interrupting contacts
var InterruptContacts runner.PreCommitHook = &interruptContacts{}

// ContactInterruption is the item for the InterruptContacts hook. NodeTallies is filled with the tally amounts which
// remove the contact's exited runs from their nodes, and should also be attached to IncrementTallies so that they're
// only incremented if the scene commits. It's refilled rather than added to, as a failed bulk commit re-runs this hook
// for each scene.
type ContactInterruption struct {
	Status      flows.SessionStatus
	NodeTallies map[string]int64
}

type interruptContacts struct{}

func (h *interruptContacts) Order() int { return 0 } // run before everything else

func (h *interruptContacts) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// group by status
	byStatus := make(map[flows.SessionStatus][]*models.Contact)
	byContact := make(map[models.ContactID]*ContactInterruption, len(scenes))
	for scene, args := range scenes {
		i := args[0].(*ContactInterruption)

		byStatus[i.Status] = append(byStatus[i.Status], scene.DBContact)
		byContact[scene.ContactID()] = i
	}

	for status, contacts := range byStatus {
		nodeTallies, err := models.InterruptContacts(ctx, tx, contacts, status)
		if err != nil {
			return fmt.Errorf("error interrupting contacts: %w", err)
		}

		for _, c := range contacts {
			i := byContact[c.ID()]
			clear(i.NodeTallies)
			maps.Copy(i.NodeTallies, nodeTallies[c.ID()])
		}
	}

	return nil
}
