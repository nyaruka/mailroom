package contact

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/urns", web.JSONPayload(handleURNs))
}

// Request to validate a set of URNs and determine ownership.
//
//	{
//	  "org_id": 1,
//	  "urns": ["tel:+593 979 123456", "webchat:123456", "line:1234567890"]
//	}
//
//	{
//	  "urns": [
//	    {"normalized": "tel:+593979123456", "contact_id": 35657, "e164": true},
//	    {"normalized": "webchat:123456", "error": "invalid path component"}
//	    {"normalized": "line:1234567890"}
//	  ]
//	}
type urnsRequest struct {
	OrgID models.OrgID `json:"org_id"   validate:"required"`
	URNs  []urns.URN   `json:"urns"  validate:"required"`
}

type urnResult struct {
	Normalized urns.URN         `json:"normalized"`
	ContactID  models.ContactID `json:"contact_id,omitempty"`
	Error      string           `json:"error,omitempty"`
	E164       bool             `json:"e164,omitempty"`
}

// handles a request to create the given contact
func handleURNs(ctx context.Context, rt *runtime.Runtime, r *urnsRequest) (any, int, error) {
	urnsToLookup := make(map[urns.URN]int, len(r.URNs)) // normalized to index of valid URNs
	results := make([]urnResult, len(r.URNs))

	for i, urn := range r.URNs {
		norm := urn.Normalize()
		scheme, path, _, _ := norm.ToParts()

		results[i].Normalized = norm

		if err := norm.Validate(); err != nil {
			results[i].Error = err.Error()
		} else {
			urnsToLookup[norm] = i
		}

		// for phone URNs, we also check if they are E164
		if scheme == urns.Phone.Prefix {
			_, err := urns.ParsePhone(path, i18n.NilCountry, false, false)
			if err == nil {
				results[i].E164 = true
			}
		}
	}

	ownerIDs, err := models.GetContactIDsFromURNs(ctx, rt.DB, r.OrgID, slices.Collect(maps.Keys(urnsToLookup)))
	if err != nil {
		return nil, 0, fmt.Errorf("error getting URN owners: %w", err)
	}

	for nurn, ownerID := range ownerIDs {
		results[urnsToLookup[nurn]].ContactID = ownerID
	}

	return map[string]any{"urns": results}, http.StatusOK, nil
}
