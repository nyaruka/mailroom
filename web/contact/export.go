package contact

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/export", web.JSONPayload(handleExport))
}

// Turns a search based export into a list of contact IDs.
//
//	{
//	  "org_id": 1,
//	  "group_id": 45,
//	  "query": "age < 65"
//	}
//
//	{
//	  "contact_ids": [73525, 3463567, 234234]
//	}
type exportRequest struct {
	OrgID   models.OrgID   `json:"org_id"   validate:"required"`
	GroupID models.GroupID `json:"group_id" validate:"required"`
	Query   string         `json:"query"`
}

type exportResponse struct {
	ContactIDs []models.ContactID `json:"contact_ids"`
}

func handleExport(ctx context.Context, rt *runtime.Runtime, r *exportRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	group := oa.GroupByID(r.GroupID)
	if group == nil {
		return errors.New("no such group"), http.StatusBadRequest, nil
	}

	ids, err := search.GetContactIDsForQuery(ctx, rt, oa, group, models.NilContactStatus, r.Query, -1)
	if err != nil {
		return nil, 0, fmt.Errorf("error querying export: %w", err)
	}

	return &exportResponse{ContactIDs: ids}, http.StatusOK, nil
}
