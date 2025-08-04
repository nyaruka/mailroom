package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/parse_query", web.JSONPayload(handleParseQuery))
}

// Request to parse the passed in query
//
//	{
//	  "org_id": 1,
//	  "query": "AGE > 10"
//	}
type parseRequest struct {
	OrgID     models.OrgID `json:"org_id"     validate:"required"`
	Query     string       `json:"query"      validate:"required"`
	ParseOnly bool         `json:"parse_only"`
}

// Response for a parse query request
//
//	{
//	  "query": "fields.age > 10",
//	  "metadata": {
//	    "fields": [
//	      {"key": "age", "name": "Age"}
//	    ],
//	    "allow_as_group": true
//	  }
//	}
type parseResponse struct {
	Query    string                `json:"query"`
	Metadata *contactql.Inspection `json:"metadata,omitempty"`
}

// handles a query parsing request
func handleParseQuery(ctx context.Context, rt *runtime.Runtime, r *parseRequest) (any, int, error) {
	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, r.OrgID, models.RefreshFields|models.RefreshGroups)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	env := oa.Env()
	var resolver contactql.Resolver
	if !r.ParseOnly {
		resolver = oa.SessionAssets()
	}

	parsed, err := contactql.ParseQuery(env, r.Query, resolver)
	if err != nil {
		return nil, 0, err
	}

	// normalize and inspect the query
	normalized := parsed.String()
	metadata := contactql.Inspect(parsed)

	return &parseResponse{Query: normalized, Metadata: metadata}, http.StatusOK, nil
}
