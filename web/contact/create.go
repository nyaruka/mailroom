package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/create", web.JSONPayload(handleCreate))
}

// Request to create a new contact.
//
//	{
//	  "org_id": 1,
//	  "user_id": 1,
//	  "contact": {
//	    "name": "Joe Blow",
//	    "language": "eng",
//	    "urns": ["tel:+250788123123"],
//	    "fields": {"age": "39"},
//	    "groups": ["b0b778db-6657-430b-9272-989ad43a10db"]
//	  }
//	}
type createRequest struct {
	OrgID   models.OrgID        `json:"org_id"   validate:"required"`
	UserID  models.UserID       `json:"user_id"  validate:"required"`
	Contact *models.ContactSpec `json:"contact"  validate:"required"`
}

// handles a request to create the given contact
func handleCreate(ctx context.Context, rt *runtime.Runtime, r *createRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	c, err := SpecToCreation(r.Contact, oa.Env(), oa.SessionAssets())
	if err != nil {
		return err, http.StatusBadRequest, nil
	}

	_, contact, err := models.CreateContact(ctx, rt.DB, oa, r.UserID, c.Name, c.Language, c.Status, c.URNs)
	if err != nil {
		return nil, 0, err
	}

	modifiersByContact := map[*flows.Contact][]flows.Modifier{contact: c.Mods}
	_, err = runner.BulkModify(ctx, rt, oa, r.UserID, modifiersByContact)
	if err != nil {
		return nil, 0, fmt.Errorf("error modifying new contact: %w", err)
	}

	return map[string]any{"contact": contact}, http.StatusOK, nil
}
