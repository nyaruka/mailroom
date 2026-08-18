package runner

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// IndexContacts is our hook for indexing contacts to Elastic after the database transaction has committed. It lives
// in this package rather than hooks so that it can be attached by packages which can't import hooks (e.g. imports).
var IndexContacts PostCommitHook = &indexContacts{}

type indexContacts struct{}

func (h *indexContacts) Order() int { return 10 }

func (h *indexContacts) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*Scene][]any) error {
	contacts := make([]*core.Contact, 0, len(scenes))
	currentFlows := make(map[models.ContactID]models.FlowID, len(scenes))
	for scene := range scenes {
		contacts = append(contacts, scene.Contact)
		currentFlows[scene.ContactID()] = scene.DBContact.CurrentFlowID()
	}

	if err := search.IndexContacts(ctx, rt, oa, contacts, currentFlows); err != nil {
		return fmt.Errorf("error indexing contacts: %w", err)
	}

	return nil
}
