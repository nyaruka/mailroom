package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nyaruka/goflow/assets"
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
// implementation. Each notification carries the UUID of the user it's for and the already-rendered JSON payload to
// publish; the rendering is Django's, so mailroom needs no knowledge of those notification types. Users are
// identified by UUID rather than looked up in the workspace's assets because they may not belong to the workspace
// (e.g. staff who serviced it).
//
//	{
//	  "org_id": 1,
//	  "notifications": [
//	    {"user_uuid": "4ac30bf6-b21a-4b90-a1a4-b09e73372dbd", "data": {"type": "export:finished", "created_on": "...", "url": "...", "is_seen": false}}
//	  ]
//	}
type publishRequest struct {
	OrgID         models.OrgID `json:"org_id"        validate:"required"`
	Notifications []struct {
		UserUUID assets.UserUUID `json:"user_uuid"`
		UserID   models.UserID   `json:"user_id"` // deprecated, only read when user_uuid isn't provided
		Data     json.RawMessage `json:"data"     validate:"required"`
	} `json:"notifications" validate:"required"`
}

func handlePublish(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets for org #%d: %w", r.OrgID, err)
	}

	items := make([]models.NotificationData, 0, len(r.Notifications))
	for i, n := range r.Notifications {
		// `validate:"required"` doesn't reject `null` or a non-object payload, but the realtime protocol assumes an
		// object, so guard here rather than forwarding e.g. literal `null` to a subscriber's socket
		if data := bytes.TrimSpace(n.Data); len(data) == 0 || data[0] != '{' {
			return fmt.Errorf("notification %d: data must be a JSON object", i), http.StatusBadRequest, nil
		}

		userUUID := n.UserUUID
		if userUUID == "" {
			// deprecated: resolve user_id via the workspace's assets, which loses notifications for users who aren't
			// members of the workspace (e.g. staff who serviced it) - that's why callers should send user_uuid
			if n.UserID == models.NilUserID {
				return fmt.Errorf("notification %d: user_uuid or user_id required", i), http.StatusBadRequest, nil
			}
			user := oa.UserByID(n.UserID)
			if user == nil {
				slog.Error("unable to publish notification for unknown user", "user_id", n.UserID, "org_id", oa.OrgID())
				continue
			}
			userUUID = user.UUID()
		}

		items = append(items, models.NotificationData{UserUUID: userUUID, Data: n.Data})
	}

	if err := models.PublishNotificationData(ctx, rt, oa, items); err != nil {
		return nil, 0, fmt.Errorf("error publishing notifications for org #%d: %w", r.OrgID, err)
	}

	return map[string]any{}, http.StatusOK, nil
}
