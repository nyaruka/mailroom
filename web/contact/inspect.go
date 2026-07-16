package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/inspect", web.JSONPayload(handleInspect))
}

// Inspects contacts.
//
//	{
//	  "org_id": 1,
//	  "contact_ids": [10000, 10001]
//	}
type inspectRequest struct {
	OrgID      models.OrgID       `json:"org_id"      validate:"required"`
	ContactIDs []models.ContactID `json:"contact_ids" validate:"required"`
}

//	{
//	  "10000": {
//	    "urns": [
//	      {
//	        "channel": {"uuid": "5a1ae059-df67-4345-922c-2fad8a2376f2", "name": "Telegram"},
//	        "scheme": "telegram",
//	        "path": "1234567876543",
//	        "display": ""
//	      },
//	      {
//	        "channel": {"uuid": "b7aa1c23-b989-4e33-bd4c-1a8511259683", "name": "Vonage"},
//	        "scheme": "tel",
//	        "path": "+1234567890",
//	        "display": ""
//	      },
//	      {
//	        "channel": null,
//	        "scheme": "twitterid",
//	        "path": "45754875854",
//	        "display": "bobby"
//	      }
//	    ]
//	  }
//	  "10001": {
//	    "urns": []
//	  }
//	}
type urnInfo struct {
	Channel *assets.ChannelReference `json:"channel"`
	Scheme  string                   `json:"scheme"`
	Path    string                   `json:"path"`
	Display string                   `json:"display"`
}

type contactInfo struct {
	URNs []urnInfo `json:"urns"`
}

func handleInspect(ctx context.Context, rt *runtime.Runtime, r *inspectRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	// load our contacts
	contacts, err := models.LoadContacts(ctx, rt.DB, oa, r.ContactIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading contact: %w", err)
	}

	response := make(map[core.ContactID]*contactInfo, len(contacts))

	for _, mc := range contacts {
		contact, err := mc.EngineContact(oa)
		if err != nil {
			return nil, 0, fmt.Errorf("error creating engine contact: %w", err)
		}

		// URNs which have a corresponding channel (engine considers these sendable) come first
		channels := oa.SessionAssets().Channels()
		sendable := make([]urnInfo, 0, len(contact.URNs()))
		unsendable := make([]urnInfo, 0, len(contact.URNs()))

		for _, u := range contact.URNs() {
			if ch := channels.GetForURN(u, assets.ChannelRoleSend); ch != nil {
				sendable = append(sendable, urnInfo{Channel: ch.Reference(), Scheme: u.Scheme, Path: u.Path})
			} else {
				unsendable = append(unsendable, urnInfo{Channel: nil, Scheme: u.Scheme, Path: u.Path, Display: u.Display})
			}
		}
		urnInfos := append(sendable, unsendable...)

		response[contact.ID()] = &contactInfo{URNs: urnInfos}
	}

	return response, http.StatusOK, nil
}
