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
	web.InternalRoute(http.MethodPost, "/contact/export_preview", web.JSONPayload(handleExportPreview))
}

// Generates a preview of which contacts will be included in an export.
//
//	{
//	  "org_id": 1,
//	  "group_id": 45,
//	  "query": "age < 65"
//	}
//
//	{
//	  "total": 567
//	}
type previewRequest struct {
	OrgID   models.OrgID   `json:"org_id"   validate:"required"`
	GroupID models.GroupID `json:"group_id" validate:"required"`
	Query   string         `json:"query"`
}

type previewResponse struct {
	Total int `json:"total"`
}

func handleExportPreview(ctx context.Context, rt *runtime.Runtime, r *previewRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	group := oa.GroupByID(r.GroupID)
	if group == nil {
		return errors.New("no such group"), http.StatusBadRequest, nil
	}

	// if there's no query, just lookup group count from db
	if r.Query == "" {
		count, err := models.GetGroupContactCount(ctx, rt.DB.DB, group.ID())
		if err != nil {
			return nil, 0, fmt.Errorf("error querying group count: %w", err)
		}
		return &previewResponse{Total: count}, http.StatusOK, nil
	}

	_, total, err := search.GetContactTotal(ctx, rt, oa, group, r.Query)
	if err != nil {
		return nil, 0, fmt.Errorf("error querying preview: %w", err)
	}

	return &previewResponse{Total: int(total)}, http.StatusOK, nil
}
