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

	// perform our search against ES (source of truth)
	esStart := time.Now()
	parsed, hits, total, err := search.GetContactIDsForQueryPage(ctx, rt, oa, group, r.ExcludeIDs, r.Query, r.Sort, r.Offset, r.Limit, false)
	if err != nil {
		return nil, 0, fmt.Errorf("error searching page: %w", err)
	}
	rt.Stats.RecordContactSearch("es", time.Since(esStart))

	// also search OpenSearch for a proportion of requests and compare results
	if rt.Config.OSContactsSearchVerify > 0 && rand.Float64() < rt.Config.OSContactsSearchVerify {
		osStart := time.Now()
		_, osHits, osTotal, osErr := search.GetContactIDsForQueryPage(ctx, rt, oa, group, r.ExcludeIDs, r.Query, r.Sort, r.Offset, r.Limit, true)
		rt.Stats.RecordContactSearch("os", time.Since(osStart))

		if osErr != nil {
			slog.Warn("error searching OpenSearch for comparison", "org_id", r.OrgID, "error", osErr)
		} else if total != osTotal || !contactIDsEqual(hits, osHits) {
			example := findMismatchExample(hits, osHits)
			slog.Error("ES/OpenSearch search mismatch",
				"org_id", r.OrgID,
				"group_id", r.GroupID,
				"query", r.Query,
				"es_total", total,
				"os_total", osTotal,
				"es_page_count", len(hits),
				"os_page_count", len(osHits),
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

// findMismatchExample returns a description of the first contact ID that differs between ES and OS results
func findMismatchExample(esIDs, osIDs []models.ContactID) string {
	osSet := make(map[models.ContactID]bool, len(osIDs))
	for _, id := range osIDs {
		osSet[id] = true
	}
	for _, id := range esIDs {
		if !osSet[id] {
			return fmt.Sprintf("contact %d in ES but not OS", id)
		}
	}

	esSet := make(map[models.ContactID]bool, len(esIDs))
	for _, id := range esIDs {
		esSet[id] = true
	}
	for _, id := range osIDs {
		if !esSet[id] {
			return fmt.Sprintf("contact %d in OS but not ES", id)
		}
	}

	return "same IDs but different order"
}
