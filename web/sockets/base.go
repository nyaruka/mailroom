package sockets

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/sockets/subscribe", web.JSONPayload(handleSubscribe))
	web.InternalRoute(http.MethodPost, "/sockets/sub_refresh", web.JSONPayload(handleSubRefresh))
}

const (
	// chatNamespace is the only client-subscribable channel namespace: a contact's chat history,
	// addressed as "chat:<contact-uuid>". A contact UUID uniquely implies its org, so the org isn't
	// in the channel name - it comes from the connection meta.
	chatNamespace = "chat"

	// subscribeWindow is how far ahead we set a subscription's expire_at, scheduling the realtime
	// server to call sub_refresh before it lapses so we can re-authorize.
	subscribeWindow = 60 * time.Second

	// subscribeTTL is how long a subscription lives in the valkey index. It's comfortably larger than
	// subscribeWindow so an entry survives between refreshes but a connection that stops refreshing
	// (e.g. the browser went away) is garbage collected.
	subscribeTTL = 90 * time.Second

	// codeForbidden is the error code returned for a denied subscription (keeps the connection alive).
	codeForbidden = 403

	// codeUnauthorized is the WebSocket close code (not an HTTP status) used to disconnect a connection
	// that has no usable identity. It's in the application-defined 4000-4999 range and mirrors HTTP 401.
	codeUnauthorized = 4401
)

// proxyRequest is the subset of a realtime server subscribe/sub_refresh proxy request we use. The server
// forwards the connection meta (set when the connection was established) at the top level on every request.
type proxyRequest struct {
	Client  string         `json:"client"`  // the connection id
	User    string         `json:"user"`    // the connection's user id
	Channel string         `json:"channel"` // the channel being subscribed to / refreshed
	Meta    connectionMeta `json:"meta"`    // the connection's identity, set when it was established
}

// connectionMeta is the identity stashed in the connection meta when the connection was established.
type connectionMeta struct {
	OrgUUID  models.OrgUUID `json:"org_uuid"`
	UserUUID string         `json:"user_uuid"`
}

// proxyResponse is a subscribe/sub_refresh proxy response. Exactly one of the fields is set.
type proxyResponse struct {
	Result     *proxyResult     `json:"result,omitempty"`
	Error      *proxyError      `json:"error,omitempty"`
	Disconnect *proxyDisconnect `json:"disconnect,omitempty"`
}

type proxyResult struct {
	ExpireAt int64 `json:"expire_at,omitempty"`
	Expired  bool  `json:"expired,omitempty"`
}

type proxyError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type proxyDisconnect struct {
	Code   int    `json:"code"`
	Reason string `json:"reason"`
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
		if err := indexSubscription(ctx, rt, now, r.Channel, r.Client); err != nil {
			return nil, 0, err
		}
		return allowed(now), http.StatusOK, nil
	case authNoIdentity:
		return disconnected(), http.StatusOK, nil
	default:
		return forbidden(), http.StatusOK, nil
	}
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

	if err := indexSubscription(ctx, rt, now, r.Channel, r.Client); err != nil {
		return nil, 0, err
	}
	return allowed(now), http.StatusOK, nil
}

// allowed builds the result for a permitted (re)subscription, setting expire_at so the realtime
// server schedules a sub_refresh before the window lapses.
func allowed(now time.Time) *proxyResponse {
	return &proxyResponse{Result: &proxyResult{ExpireAt: now.Add(subscribeWindow).Unix()}}
}

// expired builds the refresh result that lets a no-longer-authorized subscription die.
func expired() *proxyResponse {
	return &proxyResponse{Result: &proxyResult{Expired: true}}
}

// forbidden builds the error for a denied subscription, which keeps the connection alive.
func forbidden() *proxyResponse {
	return &proxyResponse{Error: &proxyError{Code: codeForbidden, Message: "forbidden"}}
}

// disconnected builds the response that closes a connection with no usable identity.
func disconnected() *proxyResponse {
	return &proxyResponse{Disconnect: &proxyDisconnect{Code: codeUnauthorized, Reason: "unauthorized"}}
}

// authResult is the outcome of authorizing a subscription.
type authResult int

const (
	authDenied     authResult = iota // recognized but not permitted - return forbidden / expire
	authAllowed                      // permitted
	authNoIdentity                   // connection meta has no usable identity - disconnect it
)

// authorize implements the default-deny allowlist for client subscriptions. The only permitted channel is a
// contact's chat history, allowed when the contact belongs to the org identified by the connection meta and
// is active. The org comes from the meta, never from the channel.
func authorize(ctx context.Context, rt *runtime.Runtime, meta connectionMeta, channel string) (authResult, error) {
	// a connection with no org/user in its meta was never authenticated by the connect proxy
	if meta.OrgUUID == "" || meta.UserUUID == "" {
		return authNoIdentity, nil
	}

	// default deny: only "chat:<contact-uuid>" is subscribable, and only with a well-formed uuid
	namespace, rest, ok := strings.Cut(channel, ":")
	if !ok || namespace != chatNamespace || !uuids.Is(rest) {
		return authDenied, nil
	}
	contactUUID := flows.ContactUUID(rest)

	// resolve the org from the connection meta
	orgID, err := models.GetOrgIDFromUUID(ctx, rt.DB.DB, meta.OrgUUID)
	if err != nil {
		return authDenied, fmt.Errorf("error resolving org from uuid: %w", err)
	}
	if orgID == models.NilOrgID {
		return authDenied, nil
	}

	// allow only if the contact belongs to that org and is active (the lookup excludes released contacts)
	ids, err := models.GetContactIDsFromUUIDs(ctx, rt.DB, orgID, []flows.ContactUUID{contactUUID})
	if err != nil {
		return authDenied, fmt.Errorf("error looking up contact: %w", err)
	}
	if len(ids) == 0 {
		return authDenied, nil
	}

	return authAllowed, nil
}
