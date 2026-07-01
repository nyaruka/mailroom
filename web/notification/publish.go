package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/notification/publish", web.JSONPayload(handlePublish))
}

// Publishes already-created notifications to their users' realtime sockets. Used by the platform's Django side to
// deliver notifications it creates itself (e.g. finished exports, locally-detected incidents) over the same realtime
// path used for notifications created here, so the socket addressing and subscriber-presence check have a single
// implementation. Each notification carries the user it's for and the already-rendered JSON payload to publish; the
// rendering is Django's, so mailroom needs no knowledge of those notification types.
//
//	{
//	  "org_id": 1,
//	  "notifications": [
//	    {"user_id": 234, "data": {"type": "export:finished", "created_on": "...", "url": "...", "is_seen": false}}
//	  ]
//	}
type publishRequest struct {
	OrgID         models.OrgID `json:"org_id"        validate:"required"`
	Notifications []struct {
		UserID models.UserID   `json:"user_id" validate:"required"`
		Data   json.RawMessage `json:"data"    validate:"required"`
	} `json:"notifications" validate:"required"`
}

func handlePublish(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets for org #%d: %w", r.OrgID, err)
	}

	items := make([]models.NotificationData, len(r.Notifications))
	for i, n := range r.Notifications {
		items[i] = models.NotificationData{UserID: n.UserID, Data: n.Data}
	}

	if err := models.PublishNotificationData(ctx, rt, oa, items); err != nil {
		return nil, 0, fmt.Errorf("error publishing notifications for org #%d: %w", r.OrgID, err)
	}

	return map[string]any{}, http.StatusOK, nil
}
