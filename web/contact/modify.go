package contact

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

var ReturnContacts = false

func init() {
	web.InternalRoute(http.MethodPost, "/contact/modify", web.JSONPayload(handleModify))
}

// Request that a set of contacts is modified.
//
//	{
//	  "org_id": 1,
//	  "user_id": 1,
//	  "contact_ids": [15,235],
//	  "modifiers": [{
//	     "type": "groups",
//	     "modification": "add",
//	     "groups": [{
//	         "uuid": "a8e8efdb-78ee-46e7-9eb0-6a578da3b02d",
//	         "name": "Doctors"
//	     }]
//	  }]
//	  "via": "ui"
//	}
type modifyRequest struct {
	OrgID      models.OrgID       `json:"org_id"      validate:"required"`
	UserID     models.UserID      `json:"user_id"     validate:"required"`
	ContactIDs []models.ContactID `json:"contact_ids" validate:"required"`
	Modifiers  []json.RawMessage  `json:"modifiers"   validate:"required"`
	Via        models.Via         `json:"via"         validate:"required,eq=api|eq=ui"`
}

// Response for contact modify. Returns events generated. Contacts that we couldn't get a lock for are returned in
// skipped. Additionally, for testing purposes only, we return the modified contacts themselves.
//
//	{
//	  "contacts": [
//	    {
//	        "uuid": "559d4cf7-8ed3-43db-9bbb-2be85345f87e",
//	        "name": "Joe",
//	        "language": "eng",
//	        ...
//	    },
//	    ...
//	  ],
//	  "events": {
//	    "559d4cf7-8ed3-43db-9bbb-2be85345f87e": [...]
//	  }
//	  "skipped": [1006, 1007]
//	}
type modifyResult struct {
	Contact *flows.Contact `json:"contact"`
	Events  []flows.Event  `json:"events"`
}

type modifyResponse struct {
	Events  map[flows.ContactUUID][]flows.Event `json:"events"`
	Skipped []models.ContactID                  `json:"skipped"`

	Contacts []*flows.Contact                 `json:"contacts,omitempty"` // testing only
	Modified map[flows.ContactID]modifyResult `json:"modified"`           // deprecated
}

// handles a request to apply the passed in actions
func handleModify(ctx context.Context, rt *runtime.Runtime, r *modifyRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	// read the modifiers from the request
	mods, err := goflow.ReadModifiers(oa.SessionAssets(), r.Modifiers, goflow.ErrorOnMissing)
	if err != nil {
		return nil, 0, err
	}

	// modifiers are the same for all contacts
	byContact := make(map[models.ContactID][]flows.Modifier, len(r.ContactIDs))
	for _, cid := range r.ContactIDs {
		byContact[cid] = mods
	}

	eventsByContact, skipped, err := runner.ModifyWithLock(ctx, rt, oa, r.UserID, r.ContactIDs, byContact, nil, r.Via)
	if err != nil {
		return nil, 0, fmt.Errorf("error bulk modifying contacts: %w", err)
	}

	events := make(map[flows.ContactUUID][]flows.Event, len(eventsByContact))
	results := make(map[flows.ContactID]modifyResult, len(r.ContactIDs))
	for flowContact, contactEvents := range eventsByContact {
		events[flowContact.UUID()] = contactEvents

		results[flowContact.ID()] = modifyResult{Contact: flowContact, Events: contactEvents}
	}

	resp := &modifyResponse{Modified: results, Events: events, Skipped: skipped}

	if ReturnContacts {
		resp.Contacts = slices.Collect(maps.Keys(eventsByContact))
		slices.SortFunc(resp.Contacts, func(a, b *flows.Contact) int { return strings.Compare(string(a.UUID()), string(b.UUID())) })
	}

	return resp, http.StatusOK, nil
}
