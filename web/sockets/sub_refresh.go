package sockets

import (
	"context"
	"net/http"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/sockets/sub_refresh", web.JSONPayload(handleSubRefresh))
}

// handleSubRefresh re-authorizes an existing subscription, since access may have been revoked since it was
// created. Refresh responses are result-only: if still allowed we extend the index entry and return a fresh
// expire_at, otherwise we mark the subscription expired so it's dropped.
func handleSubRefresh(ctx context.Context, rt *runtime.Runtime, r *proxyRequest) (any, int, error) {
	now := dates.Now()

	auth, err := authorize(ctx, rt, r.Meta, r.Channel)
	if err != nil {
		return nil, 0, err
	}

	if auth != authAllowed {
		return expired(), http.StatusOK, nil
	}

	if err := indexSubscription(ctx, rt, r.Channel, r.Client, r.Meta.UserUUID); err != nil {
		return nil, 0, err
	}
	return allowed(now), http.StatusOK, nil
}
