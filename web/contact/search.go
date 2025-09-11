package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
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
	OrgID      models.OrgID       `json:"org_id"      validate:"required"`
	GroupID    models.GroupID     `json:"group_id"    validate:"required"`
	ExcludeIDs []models.ContactID `json:"exclude_ids"`
	Query      string             `json:"query"`
	Sort       string             `json:"sort"`
	Offset     int                `json:"offset"`
	Limit      int                `json:"limit"`
}

// Response for a contact search
//
//	{
//	  "query": "age > 10",
//	  "contact_ids": [5,10,15],
//	  "total": 3,
//	  "metadata": {
//	    "fields": [
//	      {"key": "age", "name": "Age"}
//	    ],
//	    "allow_as_group": true
//	  }
//	}
type searchResponse struct {
	Query      string                `json:"query"`
	ContactIDs []models.ContactID    `json:"contact_ids"`
	Total      int64                 `json:"total"`
	Metadata   *contactql.Inspection `json:"metadata,omitempty"`
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

	// perform our search
	parsed, hits, total, err := search.GetContactIDsForQueryPage(ctx, rt, oa, group, r.ExcludeIDs, r.Query, r.Sort, r.Offset, r.Limit)
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
		Query:      normalized,
		ContactIDs: hits,
		Total:      total,
		Metadata:   metadata,
	}

	return response, http.StatusOK, nil
}
