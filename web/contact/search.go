package contact

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

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

	// perform our search against the v1 ES index (source of truth)
	v1Start := time.Now()
	parsed, hits, total, err := search.GetContactIDsForQueryPage(ctx, rt, oa, group, r.ExcludeIDs, r.Query, r.Sort, r.Offset, r.Limit, false)
	if err != nil {
		return nil, 0, fmt.Errorf("error searching page: %w", err)
	}
	rt.Stats.RecordContactSearch("v1", time.Since(v1Start))

	// also search the v2 index for a proportion of requests to verify consistency
	if rt.Config.ElasticContactsV2Verify > 0 && rand.Float64() < rt.Config.ElasticContactsV2Verify {
		v2Start := time.Now()
		_, v2Hits, v2Total, v2Err := search.GetContactIDsForQueryPage(ctx, rt, oa, group, r.ExcludeIDs, r.Query, r.Sort, r.Offset, r.Limit, true)
		rt.Stats.RecordContactSearch("v2", time.Since(v2Start))

		if v2Err != nil {
			slog.Warn("error searching v2 contacts index for comparison", "org_id", r.OrgID, "error", v2Err)
		} else if total != v2Total || !contactIDsEqual(hits, v2Hits) {
			example := findMismatchExample(hits, v2Hits)
			slog.Error("v1/v2 contacts index search mismatch",
				"org_id", r.OrgID,
				"group_id", r.GroupID,
				"query", r.Query,
				"v1_total", total,
				"v2_total", v2Total,
				"v1_page_count", len(hits),
				"v2_page_count", len(v2Hits),
				"example_contact", example,
			)
		}
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

// contactIDsEqual returns true if two slices contain the same contact IDs in the same order
func contactIDsEqual(a, b []models.ContactID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// findMismatchExample returns a description of the first contact ID that differs between v1 and v2 results
func findMismatchExample(v1IDs, v2IDs []models.ContactID) string {
	v2Set := make(map[models.ContactID]bool, len(v2IDs))
	for _, id := range v2IDs {
		v2Set[id] = true
	}
	for _, id := range v1IDs {
		if !v2Set[id] {
			return fmt.Sprintf("contact %d in v1 but not v2", id)
		}
	}

	v1Set := make(map[models.ContactID]bool, len(v1IDs))
	for _, id := range v1IDs {
		v1Set[id] = true
	}
	for _, id := range v2IDs {
		if !v1Set[id] {
			return fmt.Sprintf("contact %d in v2 but not v1", id)
		}
	}

	return "same IDs but different order"
}
