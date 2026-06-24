package sockets

import (
	"context"
	"net/http"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/sockets/subscribe", web.JSONPayload(handleSubscribe))
}

// handleSubscribe authorizes a client-initiated subscription. On allow it records the subscription in the
// valkey index and returns an expire_at so the realtime server schedules a sub_refresh. On deny it returns
// a forbidden error (connection stays up); with no usable identity it disconnects the connection.
func handleSubscribe(ctx context.Context, rt *runtime.Runtime, r *proxyRequest) (any, int, error) {
	now := dates.Now()

	auth, err := authorize(ctx, rt, r.Meta, r.Channel)
	if err != nil {
		return nil, 0, err
	}

	switch auth {
	case authAllowed:
		if err := models.RecordSubscription(ctx, rt, r.Channel, subscribeTTL); err != nil {
			return nil, 0, err
		}
		return allowed(now), http.StatusOK, nil
	case authNoIdentity:
		return disconnected(), http.StatusOK, nil
	default:
		return forbidden(), http.StatusOK, nil
	}
}
