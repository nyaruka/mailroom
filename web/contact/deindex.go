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
	web.InternalRoute(http.MethodPost, "/contact/deindex", web.JSONPayload(handleDeindex))
}

// Requests de-indexing of the given contacts from Elastic indexes.
//
//	{
//	  "org_id": 1,
//	  "contact_uuids": ["548f43fb-f32a-491f-abb7-0c29a453a06e"]
//	}
type deindexRequest struct {
	OrgID        models.OrgID        `json:"org_id"        validate:"required"`
	ContactUUIDs []flows.ContactUUID `json:"contact_uuids" validate:"required"`
}

func handleDeindex(ctx context.Context, rt *runtime.Runtime, r *deindexRequest) (any, int, error) {
	deindexed, err := search.DeindexContactsByUUID(ctx, rt, r.OrgID, r.ContactUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error de-indexing contacts in org #%d: %w", r.OrgID, err)
	}

	if _, err := search.DeindexMessagesByContact(ctx, rt, r.OrgID, r.ContactUUIDs); err != nil {
		return nil, 0, fmt.Errorf("error de-indexing messages in org #%d: %w", r.OrgID, err)
	}

	return map[string]any{"deindexed": deindexed}, http.StatusOK, nil
}
