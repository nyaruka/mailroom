package msg

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/search", web.JSONPayload(handleSearch))
}

// Searches messages in the OpenSearch messages index
//
//	{
//	  "org_id": 1,
//	  "text": "hello"
//	}
type searchRequest struct {
	OrgID models.OrgID `json:"org_id" validate:"required"`
	Text  string       `json:"text"   validate:"required"`
}

func handleSearch(ctx context.Context, rt *runtime.Runtime, r *searchRequest) (any, int, error) {
	_, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	msgs, total, err := search.SearchMessages(ctx, rt, r.OrgID, r.Text)
	if err != nil {
		return nil, 0, fmt.Errorf("error searching messages: %w", err)
	}

	return map[string]any{
		"total":    total,
		"messages": msgs,
	}, http.StatusOK, nil
}
