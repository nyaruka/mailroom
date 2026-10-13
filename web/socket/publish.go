package socket

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/courier"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/nyaruka/vkutil"
)

func init() {
	web.InternalRoute(http.MethodPost, "/socket/publish", web.JSONPayload(handlePublish))
}

// how long we're willing to wait on courier before giving up on forwarding - the whole request has to fit
// inside centrifugo's proxy timeout
const courierTimeout = 2 * time.Second

// visitors can publish this many typing events per chat per minute - well above what a widget pulsing while its
// visitor composes needs
var chatTypingLimiter = vkutil.NewRateLimiter("chat-typing", 30, time.Minute)

// matches the chat sockets the platform lets visitors subscribe to, i.e. chat:<channel-uuid>:<chat-id>
var chatSocketRegex = regexp.MustCompile(`^chat:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}):([a-zA-Z0-9]{24})$`)

// Handles a client publication proxied by the centrifugo server. Domain denials are returned in-body with HTTP 200
// as the proxy protocol requires.
//
//	{
//	  "client": "9336a229-2400-4382-8d27-9ec18b28219c",
//	  "user": "3",
//	  "channel": "history:a393abc0-283d-4c9b-a1b3-641a035c34bf",
//	  "data": {"type": "typing_started"},
//	  "meta": {"user_id": 3, "user_uuid": "ad9f...", "org_id": 1, "org_uuid": "bf05..."}
//	}
type publishRequest struct {
	Channel string          `json:"channel" validate:"required"`
	Data    json.RawMessage `json:"data"    validate:"required"`
	Meta    *publishMeta    `json:"meta"`
}

// connection meta stamped by the connect proxy - requires include_connection_meta in the centrifugo proxy config
type publishMeta struct {
	UserID   models.UserID   `json:"user_id"`
	UserUUID assets.UserUUID `json:"user_uuid"`
	OrgID    models.OrgID    `json:"org_id"`
	OrgUUID  models.OrgUUID  `json:"org_uuid"`
}

// what agent clients publish to a contact's history socket. Only the event type and msg_external_id are read -
// everything else (uuid, created_on, direction, channel/urn routing, user attribution) is stamped server-side.
// msg_external_id is the platform's own identifier of the newest incoming message, which only clients have at
// hand (from msg_received events) and which some platforms require typing indicators to reference.
type typingData struct {
	Type          string `json:"type"`
	MsgExternalID string `json:"msg_external_id"`
}

type publishResult struct {
	Data        any  `json:"data"`
	SkipHistory bool `json:"skip_history"`
}

type publishError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type publishResponse struct {
	Result *publishResult `json:"result,omitempty"`
	Error  *publishError  `json:"error,omitempty"`
}

func deny(code int, msg string) (any, int, error) {
	return &publishResponse{Error: &publishError{Code: code, Message: msg}}, http.StatusOK, nil
}

func handlePublish(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	// this handler is the sole publish authorizer: when a publish proxy is enabled centrifugo skips all of its
	// built-in permission checks, including whether the publisher is even subscribed to the socket
	namespace, _, _ := strings.Cut(r.Channel, ":")
	switch namespace {
	case models.SocketHistoryNamespace:
		return publishToHistory(ctx, rt, r)
	case models.SocketChatNamespace:
		return publishToChat(ctx, rt, r)
	}
	return deny(403, "publishing not supported on this socket")
}

func readTypingData(raw json.RawMessage) (*typingData, string) {
	data := &typingData{}
	if err := json.Unmarshal(raw, data); err != nil {
		return nil, "invalid publication data"
	}
	if data.Type != events.TypeTypingStarted && data.Type != events.TypeTypingStopped {
		return nil, fmt.Sprintf("unsupported publication type: %s", data.Type)
	}
	return data, ""
}

// handles agents publishing typing to a contact's history socket. Agents are authorized against the connection meta
// stamped server-side at connect time.
func publishToHistory(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	if r.Meta == nil {
		slog.Error("socket publication without connection meta, check that centrifugo proxy sets include_connection_meta")
		return deny(403, "no connection meta")
	}
	if r.Meta.UserID == models.NilUserID || r.Meta.OrgID == models.NilOrgID {
		return deny(403, "anonymous connection")
	}

	// only contact level history sockets accept publications, i.e. not ticket scoped ones
	parts := strings.Split(r.Channel, ":")
	if len(parts) != 2 || !uuids.Is(parts[1]) {
		return deny(403, "publishing not supported on this socket")
	}
	contactUUID := core.ContactUUID(parts[1])

	data, msg := readTypingData(r.Data)
	if data == nil {
		return deny(403, msg)
	}

	oa, err := models.GetOrgAssets(ctx, rt, r.Meta.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	// check that the publisher is a user in the org, and that the contact whose socket this is and the chat channel
	// both belong to that org
	user := oa.UserByID(r.Meta.UserID)
	if user == nil {
		return deny(403, "no such user")
	}
	contacts, err := models.LoadContactsByUUID(ctx, rt.DB, oa, []core.ContactUUID{contactUUID})
	if err != nil {
		return nil, 0, fmt.Errorf("error loading contact: %w", err)
	}
	if len(contacts) == 0 {
		return deny(403, "no such contact")
	}
	contact, err := contacts[0].EngineContact(oa)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating engine contact: %w", err)
	}

	// resolve channel + URN the same way a reply to this contact would route, so typing shows up in the same
	// conversation the reply will land in. An unreachable contact has nowhere to send typing, but the event
	// still fans out to other users for co-presence.
	var channel *models.Channel
	var channelRef *assets.ChannelReference
	urn := urns.NilURN
	if r := contact.ResolveRoute(); r != nil {
		channel = oa.ChannelByUUID(r.Channel.UUID())
		channelRef = r.Channel.Reference()
		urn = r.URN
	}

	// rewrite the publication as a server-stamped event - our own uuid, created_on and direction, the routing
	// we resolved, and the typist attributed
	var event events.Event
	if data.Type == events.TypeTypingStarted {
		event = events.NewTypingStarted(events.DirectionOutgoing, channelRef, urn, data.MsgExternalID)
	} else {
		event = events.NewTypingStopped(events.DirectionOutgoing, channelRef, urn, data.MsgExternalID)
	}
	event.SetUser(user.Reference(), string(models.ViaUI))

	// forward to courier best effort - agent-to-agent co-presence is valid even if the platform call fails, and
	// courier throttles platform sends per conversation itself so every pulse is forwarded as-is. Capability
	// stays courier's concern: events it can't relay (e.g. typing_stopped on all channels today) just come back
	// unsupported, and the fan-out to other users still has co-presence value.
	if channel != nil {
		fwdCtx, cancel := context.WithTimeout(ctx, courierTimeout)
		defer cancel()
		if _, err := courier.SendEvent(fwdCtx, rt, channel, event); err != nil {
			slog.Error("error sending event to courier", "error", err, "channel", channel.UUID())
		}
	}

	// unlike everything else published to history sockets this event is ephemeral and never persisted, hence
	// skip_history
	return &publishResponse{Result: &publishResult{Data: event, SkipHistory: true}}, http.StatusOK, nil
}

// what the visitor's own chat socket carries back to its tabs - courier's typing shape, but without the user which
// marks an agent typing, so that widgets can tell their own echo apart
type chatTypingEcho struct {
	Type      string    `json:"type"`
	CreatedOn time.Time `json:"created_on"`
}

// handles webchat visitors publishing typing to their chat socket. Visitors connect anonymously, so as with
// courier's webchat endpoints and the platform's authorization of chat socket subscriptions, possession of the chat
// ID is the credential.
func publishToChat(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	match := chatSocketRegex.FindStringSubmatch(r.Channel)
	if match == nil {
		return deny(403, "publishing not supported on this socket")
	}
	channelUUID, chatID := assets.ChannelUUID(match[1]), match[2]

	data, msg := readTypingData(r.Data)
	if data == nil {
		return deny(403, msg)
	}

	visitor, err := models.GetWebChatVisitor(ctx, channelUUID, chatID)
	if err != nil {
		return nil, 0, err
	}
	if visitor == nil {
		return deny(403, "no such chat")
	}

	// throttled only once the chat is known to exist, so that made-up chat IDs can't create keys
	if !allowChatTyping(ctx, rt, channelUUID, chatID) {
		return deny(429, "too many publications")
	}

	oa, err := models.GetOrgAssets(ctx, rt, visitor.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	// org assets only include enabled channels, and are refreshed more often than visitors
	channel := oa.ChannelByUUID(channelUUID)
	if channel == nil {
		return deny(403, "no such chat")
	}

	urn, err := urns.New(urns.WebChat, chatID)
	if err != nil {
		return deny(403, "no such chat")
	}

	var event events.Event
	if data.Type == events.TypeTypingStarted {
		event = events.NewTypingStarted(events.DirectionIncoming, channel.Reference(), urn, "")
	} else {
		event = events.NewTypingStopped(events.DirectionIncoming, channel.Reference(), urn, "")
	}

	// best effort like all history publishing - the visitor's publication still stands
	if err := models.PublishToHistory(ctx, rt, visitor.ContactUUID, []events.Event{event}); err != nil {
		slog.Error("error publishing visitor typing", "error", err, "contact", visitor.ContactUUID)
	}

	echo := &chatTypingEcho{Type: data.Type, CreatedOn: event.CreatedOn()}
	return &publishResponse{Result: &publishResult{Data: echo, SkipHistory: true}}, http.StatusOK, nil
}

// fails open since a valkey problem shouldn't stop visitors' typing reaching agents
func allowChatTyping(ctx context.Context, rt *runtime.Runtime, channelUUID assets.ChannelUUID, chatID string) bool {
	vc := rt.VK.Get()
	defer vc.Close()

	allowed, err := chatTypingLimiter.Allow(ctx, vc, fmt.Sprintf("%s|%s", channelUUID, chatID))
	if err != nil {
		slog.Error("error checking chat typing rate limit", "error", err, "channel", channelUUID)
		return true
	}
	return allowed
}
