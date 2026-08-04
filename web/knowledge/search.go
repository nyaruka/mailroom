package knowledge

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/knowledge/search", web.JSONPayload(handleSearch))
}

// Searches the org's knowledge sources semantically.
//
//	{
//	  "org_id": 1,
//	  "query": "how do I get a refund?",
//	  "limit": 10
//	}
type searchRequest struct {
	OrgID models.OrgID `json:"org_id" validate:"required"`
	Query string       `json:"query"  validate:"required"`
	Limit int          `json:"limit"`
}

// Response is the matching chunks, best first.
//
//	{
//	  "results": [
//	    {
//	      "knowledge_uuid": "97180291-8d95-4a6b-8a1a-63c44bb84b77",
//	      "item_key": "e0d47f61-9531-46a5-89dd-8e8437bee883",
//	      "item_name": "Refunds",
//	      "text": "We offer full refunds within 30 days...",
//	      "score": 0.9034
//	    }
//	  ]
//	}
type searchResponse struct {
	Results []*knowledge.SearchResult `json:"results"`
}

func handleSearch(ctx context.Context, rt *runtime.Runtime, r *searchRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	if r.Limit == 0 {
		r.Limit = 10
	}

	results, err := knowledge.Search(ctx, rt, oa, r.Query, r.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("error searching knowledge: %w", err)
	}

	return &searchResponse{Results: results}, http.StatusOK, nil
}
