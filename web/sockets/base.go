package sockets

import (
	"context"
	"fmt"
	"strings"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const (
	// chatNamespace is the only client-subscribable channel namespace: a contact's chat history,
	// addressed as "chat:<contact-uuid>". A contact UUID uniquely implies its org, so the org isn't
	// in the channel name - it comes from the connection meta.
	chatNamespace = "chat"

	// subscribeWindow is how far ahead we set a subscription's expire_at, scheduling the realtime
	// server to call sub_refresh before it lapses so we can re-authorize.
	subscribeWindow = 60 * time.Second

	// subscribeTTL is how long a channel's presence key survives without a refresh. It must comfortably
	// exceed subscribeWindow plus the realtime server's refresh delay: the server drives sub_refresh from
	// the expire_at we return (subscribeWindow), but Centrifugo's docs note refresh requests can be delayed
	// up to ~1 minute. So consecutive refreshes can be as much as subscribeWindow + ~60s apart; 150s (60s
	// window + ~60s delay + buffer) keeps the key alive across that gap, while still expiring within a
	// couple of minutes once the last subscriber stops refreshing (there's no unsubscribe callback, so this
	// TTL is the only GC).
	subscribeTTL = 150 * time.Second

	// codeForbidden is the error code returned for a denied subscription (keeps the connection alive).
	codeForbidden = 403

	// codeUnauthorized is the WebSocket close code (not an HTTP status) used to disconnect a connection
	// that has no usable identity. It's in the application-defined 4000-4999 range and mirrors HTTP 401.
	codeUnauthorized = 4401
)

// proxyRequest is the subset of a realtime server subscribe/sub_refresh proxy request we use. The server
// forwards the connection meta (set when the connection was established) at the top level on every request.
type proxyRequest struct {
	Client  string         `json:"client"`                      // the connection id
	User    string         `json:"user"`                        // the connection's user id
	Channel string         `json:"channel" validate:"required"` // the channel being subscribed to / refreshed
	Meta    connectionMeta `json:"meta"`                        // the connection's identity, set when it was established
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
// isn't released (soft-deleted). Blocked/stopped/archived contacts still have viewable history, so they're
// allowed too. The org comes from the meta, never from the channel.
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
		return 0, fmt.Errorf("error resolving org from uuid: %w", err)
	}
	if orgID == models.NilOrgID {
		return authDenied, nil
	}

	// allow only if the contact belongs to that org and isn't released (the lookup excludes soft-deleted
	// contacts; blocked/stopped/archived ones are still returned and remain viewable)
	ids, err := models.GetContactIDsFromUUIDs(ctx, rt.DB, orgID, []flows.ContactUUID{contactUUID})
	if err != nil {
		return 0, fmt.Errorf("error looking up contact: %w", err)
	}
	if len(ids) == 0 {
		return authDenied, nil
	}

	return authAllowed, nil
}

// subKey is the valkey key marking that a channel has at least one active subscription, e.g.
// "socket-subs:chat:<contact-uuid>".
func subKey(channel string) string {
	return fmt.Sprintf("socket-subs:%s", channel)
}

// indexSubscription marks a channel as having at least one active subscription, with a TTL. Every subscribe
// and sub_refresh from any connection re-sets the same per-channel key, so the key stays present as long as
// some subscriber keeps refreshing it; once the last one goes away (the realtime server has no unsubscribe
// or disconnect callback) nobody refreshes it and it expires. The backend only needs to know whether a
// contact's chat might have subscribers - not who or how many - so a single presence key per channel is all
// we keep.
func indexSubscription(ctx context.Context, rt *runtime.Runtime, channel string) error {
	vc := rt.VK.Get()
	defer vc.Close()

	ttl := int(subscribeTTL / time.Second)
	if _, err := valkey.DoContext(vc, ctx, "SET", subKey(channel), "1", "EX", ttl); err != nil {
		return fmt.Errorf("error updating subscription index for %s: %w", channel, err)
	}

	return nil
}
