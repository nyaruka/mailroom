package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/search", web.JSONPayload(handleSearch))
}

// Searches the contacts in an org
//
//	{
//	  "org_id": 1,
//	  "group_id": 234,
//	  "query": "age > 10",
//	  "sort": "-age",
//	  "offset": 0,
//	  "limit": 50
//	}
type searchRequest struct {
	OrgID        models.OrgID        `json:"org_id"      validate:"required"`
	GroupID      models.GroupID      `json:"group_id"    validate:"required"`
	ExcludeUUIDs []flows.ContactUUID `json:"exclude_uuids"`
	Query        string              `json:"query"`
	Sort         string              `json:"sort"`
	Offset       int                 `json:"offset"`
	Limit        int                 `json:"limit"`
}

// Response for a contact search
//
//	{
//	  "query": "age > 10",
//	  "contact_uuids": ["b699a406-7e44-49be-9f01-1a82893e8a10"],
//	  "total": 1,
//	  "metadata": {
//	    "fields": [
//	      {"key": "age", "name": "Age"}
//	    ],
//	    "allow_as_group": true
//	  }
//	}
type searchResponse struct {
	Query        string                `json:"query"`
	ContactUUIDs []flows.ContactUUID   `json:"contact_uuids"`
	Total        int64                 `json:"total"`
	Metadata     *contactql.Inspection `json:"metadata,omitempty"`
}

// handles a contact search request
func handleSearch(ctx context.Context, rt *runtime.Runtime, r *searchRequest) (any, int, error) {
	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, r.OrgID, models.RefreshFields|models.RefreshGroups)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	group := oa.GroupByID(r.GroupID)
	if r.Limit == 0 {
		r.Limit = 50
	}

	parsed, hits, total, err := search.GetContactUUIDsForQueryPage(ctx, rt, oa, group, r.ExcludeUUIDs, r.Query, r.Sort, r.Offset, r.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("error searching page: %w", err)
	}

	// normalize and inspect the query
	normalized := ""
	var metadata *contactql.Inspection

	if parsed != nil {
		normalized = parsed.String()
		metadata = contactql.Inspect(parsed)
	}

	// build our response
	response := &searchResponse{
		Query:        normalized,
		ContactUUIDs: hits,
		Total:        total,
		Metadata:     metadata,
	}

	return response, http.StatusOK, nil
}
