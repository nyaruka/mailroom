package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/deindex", web.JSONPayload(handleDeindex))
}

// Requests de-indexing of the given contacts from Elastic indexes.
//
//	{
//	  "org_id": 1,
//	  "contact_ids": [12345, 23456]
//	}
type deindexRequest struct {
	OrgID      models.OrgID       `json:"org_id"  validate:"required"`
	ContactIDs []models.ContactID `json:"contact_ids" validate:"required"`
}

// handles a request to resend the given messages
func handleDeindex(ctx context.Context, rt *runtime.Runtime, r *deindexRequest) (any, int, error) {
	deindexed, err := search.DeindexContactsByID(ctx, rt, r.OrgID, r.ContactIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error de-indexing contacts in org #%d: %w", r.OrgID, err)
	}

	return map[string]any{"deindexed": deindexed}, http.StatusOK, nil
}
