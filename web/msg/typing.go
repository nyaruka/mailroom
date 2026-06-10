package msg

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/msgio"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/typing", web.JSONPayload(handleTyping))
}

// Request to send a typing indicator to a contact. This is a no-op if the contact's preferred channel doesn't
// support typing indicators.
//
//	{
//	  "org_id": 1,
//	  "contact_id": 123456
//	}
type typingRequest struct {
	OrgID     models.OrgID     `json:"org_id"     validate:"required"`
	ContactID models.ContactID `json:"contact_id" validate:"required"`
}

// handles a request to send a typing indicator to a contact
func handleTyping(ctx context.Context, rt *runtime.Runtime, r *typingRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	c, err := models.LoadContact(ctx, rt.DB, oa, r.ContactID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading contact: %w", err)
	}

	contact, err := c.EngineContact(oa)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating flow contact: %w", err)
	}

	// resolve the same URN + channel that a send would use, and check that it supports typing indicators
	for _, route := range contact.ResolveRoutes(false) {
		ch := oa.ChannelByUUID(route.Channel.UUID())
		if ch != nil && slices.Contains(ch.Features(), models.ChannelFeatureTyping) {
			if err := msgio.SendTyping(ctx, rt, ch, route.URN); err != nil {
				return nil, 0, fmt.Errorf("error sending typing indicator: %w", err)
			}
		}
		break
	}

	return map[string]any{}, http.StatusOK, nil
}
