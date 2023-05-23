package flow

import (
	"context"
	"net/http"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
	"github.com/pkg/errors"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/flow/preview_start", web.RequireAuthToken(web.JSONPayload(handlePreviewStart)))
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
//	  },
//	  "sample_size": 5
//	}
//
//	{
//	  "query": "(group = "No Age" OR group = "No Name" OR uuid = "e5bb9e6f-7703-4ba1-afba-0b12791de38b") AND history != \"Registration\"",
//	  "total": 567,
//	  "sample": [12, 34, 56, 67, 78],
//	  "metadata": {
//	    "fields": [
//	      {"key": "age", "name": "Age"}
//	    ],
//	    "allow_as_group": true
//	  }
//	}
type previewStartRequest struct {
	OrgID   models.OrgID  `json:"org_id"    validate:"required"`
	FlowID  models.FlowID `json:"flow_id"   validate:"required"`
	Include struct {
		GroupUUIDs   []assets.GroupUUID  `json:"group_uuids"`
		ContactUUIDs []flows.ContactUUID `json:"contact_uuids"`
		Query        string              `json:"query"`
	} `json:"include"   validate:"required"`
	Exclude    models.Exclusions `json:"exclude"`
	SampleSize int               `json:"sample_size"  validate:"required"`
}

type previewStartResponse struct {
	Query     string                `json:"query"`
	Total     int                   `json:"total"`
	SampleIDs []models.ContactID    `json:"sample_ids"`
	Metadata  *contactql.Inspection `json:"metadata,omitempty"`
}

func handlePreviewStart(ctx context.Context, rt *runtime.Runtime, r *previewStartRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "unable to load org assets")
	}

	flow, err := oa.FlowByID(r.FlowID)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "unable to load flow")
	}

	groups := make([]*models.Group, 0, len(r.Include.GroupUUIDs))
	for _, groupUUID := range r.Include.GroupUUIDs {
		g := oa.GroupByUUID(groupUUID)
		if g != nil {
			groups = append(groups, g)
		}
	}

	query, err := search.BuildStartQuery(oa, flow, groups, r.Include.ContactUUIDs, r.Include.Query, r.Exclude, nil)
	if err != nil {
		isQueryError, qerr := contactql.IsQueryError(err)
		if isQueryError {
			return qerr, http.StatusBadRequest, nil
		}
		return nil, 0, err
	}
	if query == "" {
		return &previewStartResponse{SampleIDs: []models.ContactID{}}, http.StatusOK, nil
	}

	parsedQuery, sampleIDs, total, err := search.GetContactIDsForQueryPage(ctx, rt, oa, nil, nil, query, "", 0, r.SampleSize)
	if err != nil {
		return nil, 0, errors.Wrap(err, "error querying preview")
	}

	inspection := contactql.Inspect(parsedQuery)

	return &previewStartResponse{
		Query:     parsedQuery.String(),
		Total:     int(total),
		SampleIDs: sampleIDs,
		Metadata:  inspection,
	}, http.StatusOK, nil
}
