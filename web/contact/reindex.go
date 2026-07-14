package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/reindex", web.JSONPayload(handleReindex))
}

// Loads the given contacts from the database and reindexes them in Elastic.
//
//	{
//	  "org_id": 1,
//	  "contact_uuids": ["b699a406-7e44-49be-9f01-1a82893e8a10", "cd024bcd-f473-4719-a00a-bd0bb1190135"]
//	}
type reindexRequest struct {
	OrgID        models.OrgID       `json:"org_id"         validate:"required"`
	ContactUUIDs []core.ContactUUID `json:"contact_uuids"  validate:"required"`
}

func handleReindex(ctx context.Context, rt *runtime.Runtime, r *reindexRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	mcs, err := models.LoadContactsByUUID(ctx, rt.DB, oa, r.ContactUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading contacts: %w", err)
	}

	contacts := make([]*core.Contact, 0, len(mcs))
	currentFlows := make(map[models.ContactID]models.FlowID, len(mcs))
	for _, mc := range mcs {
		contact, err := mc.EngineContact(oa)
		if err != nil {
			return nil, 0, fmt.Errorf("error creating engine contact: %w", err)
		}
		contacts = append(contacts, contact)
		currentFlows[mc.ID()] = mc.CurrentFlowID()
	}

	if err := search.IndexContacts(ctx, rt, oa, contacts, currentFlows); err != nil {
		return nil, 0, fmt.Errorf("error indexing contacts: %w", err)
	}

	return map[string]any{"indexed": len(mcs)}, http.StatusOK, nil
}
