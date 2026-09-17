package contact

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/urns", web.JSONPayload(handleURNs))
}

// Request to normalize and validate a set of URNs and determine ownership. Phone numbers are parsed using the org's
// default country so local numbers become E164 where possible, and e164 is set for phone URNs which are E164 numbers
// (i.e. not short codes or sender IDs). Ownership lookups can be skipped with validate_only, e.g. for large batches
// where only the normalized forms are needed.
//
//	{
//	  "org_id": 1,
//	  "urns": ["tel:+593 979 123456", "tel:(605) 574-2222", "webchat:123456", "line:1234567890"],
//	  "validate_only": false
//	}
//
//	{
//	  "urns": [
//	    {"normalized": "tel:+593979123456", "contact_id": 35657, "e164": true},
//	    {"normalized": "tel:+16055742222", "e164": true},
//	    {"normalized": "webchat:123456", "error": "invalid path component"}
//	    {"normalized": "line:1234567890"}
//	  ]
//	}
type urnsRequest struct {
	OrgID        models.OrgID `json:"org_id"        validate:"required"`
	URNs         []urns.URN   `json:"urns"          validate:"required"`
	ValidateOnly bool         `json:"validate_only"`
}

type urnResult struct {
	Normalized urns.URN         `json:"normalized"`
	ContactID  models.ContactID `json:"contact_id,omitempty"`
	Error      string           `json:"error,omitempty"`
	E164       bool             `json:"e164,omitempty"`
}

// handles a request to normalize and validate the given URNs
func handleURNs(ctx context.Context, rt *runtime.Runtime, r *urnsRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	urnsToLookup := make(map[urns.URN][]int, len(r.URNs)) // normalized to indexes of valid URNs
	results := make([]urnResult, len(r.URNs))

	for i, urn := range r.URNs {
		norm, e164, err := models.NormalizeURN(urn, oa.Env().DefaultCountry())

		results[i].Normalized = norm
		results[i].E164 = e164

		if err != nil {
			results[i].Error = err.Error()
		} else {
			urnsToLookup[norm] = append(urnsToLookup[norm], i)
		}
	}

	if !r.ValidateOnly {
		ownerIDs, err := models.GetContactIDsFromURNs(ctx, rt.DB, r.OrgID, slices.Collect(maps.Keys(urnsToLookup)))
		if err != nil {
			return nil, 0, fmt.Errorf("error getting URN owners: %w", err)
		}

		for nurn, ownerID := range ownerIDs {
			for _, i := range urnsToLookup[nurn] {
				results[i].ContactID = ownerID
			}
		}
	}

	return map[string]any{"urns": results}, http.StatusOK, nil
}
