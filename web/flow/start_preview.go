package flow

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/flow/start_preview", web.JSONPayload(handleStartPreview))
}

// Generates a preview of which contacts will be started in the given flow.
//
//	{
//	  "org_id": 1,
//	  "flow_id": 2,
//	  "include": {
//	    "group_uuids": ["5fa925e4-edd8-4e2a-ab24-b3dbb5932ddd", "2912b95f-5b89-4d39-a2a8-5292602f357f"],
//	    "contact_uuids": ["e5bb9e6f-7703-4ba1-afba-0b12791de38b"],
//	    "query": ""
//	  },
//	  "exclude": {
//	    "non_active": false,
//	    "in_a_flow": false,
//	    "started_previously": true,
//	    "not_seen_since_days": 90
//	  }
//	}
//
//	{
//	  "query": "(group = \"No Age\" OR group = \"No Name\" OR uuid = \"e5bb9e6f-7703-4ba1-afba-0b12791de38b\") AND history != \"Registration\"",
//	  "total": 567
//	}
type previewRequest struct {
	OrgID   models.OrgID  `json:"org_id"    validate:"required"`
	FlowID  models.FlowID `json:"flow_id"   validate:"required"`
	Include struct {
		GroupUUIDs   []assets.GroupUUID  `json:"group_uuids"`
		ContactUUIDs []flows.ContactUUID `json:"contact_uuids"`
		Query        string              `json:"query"`
	} `json:"include"   validate:"required"`
	Exclude models.Exclusions `json:"exclude"`
}

type previewResponse struct {
	Query string `json:"query"`
	Total int    `json:"total"`
}

func handleStartPreview(ctx context.Context, rt *runtime.Runtime, r *previewRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	flow, err := oa.FlowByID(r.FlowID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load flow: %w", err)
	}

	groups := make([]*models.Group, 0, len(r.Include.GroupUUIDs))
	for _, groupUUID := range r.Include.GroupUUIDs {
		g := oa.GroupByUUID(groupUUID)
		if g != nil {
			groups = append(groups, g)
		}
	}

	query, err := search.BuildRecipientsQuery(oa, flow, groups, r.Include.ContactUUIDs, r.Include.Query, r.Exclude, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("error building query: %w", err)
	}
	if query == "" {
		return &previewResponse{Query: "", Total: 0}, http.StatusOK, nil
	}

	parsedQuery, total, err := search.GetContactTotal(ctx, rt, oa, nil, query)
	if err != nil {
		return nil, 0, fmt.Errorf("error querying preview: %w", err)
	}

	return &previewResponse{Query: parsedQuery.String(), Total: int(total)}, http.StatusOK, nil
}
