package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
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
	OrgID        models.OrgID        `json:"org_id"         validate:"required"`
	ContactUUIDs []flows.ContactUUID `json:"contact_uuids"  validate:"required"`
}

func handleReindex(ctx context.Context, rt *runtime.Runtime, r *reindexRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	contacts, err := models.LoadContactsByUUID(ctx, rt.DB, oa, r.ContactUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading contacts: %w", err)
	}

	flowContacts := make([]*flows.Contact, 0, len(contacts))
	currentFlows := make(map[models.ContactID]models.FlowID, len(contacts))
	for _, c := range contacts {
		fc, err := c.EngineContact(oa)
		if err != nil {
			return nil, 0, fmt.Errorf("error creating flow contact: %w", err)
		}
		flowContacts = append(flowContacts, fc)
		currentFlows[c.ID()] = c.CurrentFlowID()
	}

	if err := search.IndexContacts(ctx, rt, oa, flowContacts, currentFlows); err != nil {
		return nil, 0, fmt.Errorf("error indexing contacts: %w", err)
	}

	return map[string]any{"indexed": len(contacts)}, http.StatusOK, nil
}
