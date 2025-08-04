package org

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/org/deindex", web.JSONPayload(handleDeindex))
}

// Requests de-indexing of the given org from Elastic indexes.
//
//	{
//	  "org_id": 1
//	}
type deindexRequest struct {
	OrgID models.OrgID `json:"org_id"  validate:"required"`
}

// handles a request to resend the given messages
func handleDeindex(ctx context.Context, rt *runtime.Runtime, r *deindexRequest) (any, int, error) {
	// check that org exists and is not active
	var isActive bool
	if err := rt.DB.Get(&isActive, `SELECT is_active FROM orgs_org WHERE id = $1`, r.OrgID); err != nil {
		return nil, 0, fmt.Errorf("error querying org #%d: %w", r.OrgID, err)
	}
	if isActive {
		return nil, 0, fmt.Errorf("can't deindex active org #%d", r.OrgID)
	}

	if err := crons.MarkOrgForDeindexing(ctx, rt, r.OrgID); err != nil {
		return nil, 0, fmt.Errorf("error marking org #%d for de-indexing: %w", r.OrgID, err)
	}

	return map[string]any{}, http.StatusOK, nil
}
