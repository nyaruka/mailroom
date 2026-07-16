package socket

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
)

func init() {
	web.InternalRoute(http.MethodPost, "/socket/publish", web.JSONPayload(handlePublish))
}

// how long we're willing to wait on courier before giving up on forwarding - the whole request has to fit
// inside centrifugo's proxy timeout
const courierTimeout = 2 * time.Second

// Handles a client publication proxied by the centrifugo server. The connection meta is stamped server-side at
// connect time and is what publications are authorized against. Domain denials are returned in-body with HTTP 200
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

func handlePublish(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	deny := func(msg string) (any, int, error) {
		return &publishResponse{Error: &publishError{Code: 403, Message: msg}}, http.StatusOK, nil
	}

	if r.Meta == nil {
		slog.Error("socket publication without connection meta, check that centrifugo proxy sets include_connection_meta")
		return deny("no connection meta")
	}

	// only contact level history sockets accept publications, i.e. not ticket scoped ones
	parts := strings.Split(r.Channel, ":")
	if len(parts) != 2 || parts[0] != models.SocketHistoryNamespace || !uuids.Is(parts[1]) {
		return deny("publishing not supported on this socket")
	}
	contactUUID := core.ContactUUID(parts[1])

	data := &typingData{}
	if err := json.Unmarshal(r.Data, data); err != nil {
		return deny("invalid publication data")
	}
	if data.Type != events.TypeTypingStarted && data.Type != events.TypeTypingStopped {
		return deny(fmt.Sprintf("unsupported publication type: %s", data.Type))
	}

	oa, err := models.GetOrgAssets(ctx, rt, r.Meta.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	// this handler is the sole publish authorizer: when a publish proxy is enabled centrifugo skips all of its
	// built-in permission checks, including whether the publisher is even subscribed to the socket. So check that
	// the publisher is a user in the org, and that the contact whose socket this is and the chat channel both
	// belong to that org.
	user := oa.UserByID(r.Meta.UserID)
	if user == nil {
		return deny("no such user")
	}
	contacts, err := models.LoadContactsByUUID(ctx, rt.DB, oa, []core.ContactUUID{contactUUID})
	if err != nil {
		return nil, 0, fmt.Errorf("error loading contact: %w", err)
	}
	if len(contacts) == 0 {
		return deny("no such contact")
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
	for _, r := range contact.ResolveRoutes(false) {
		channel = oa.ChannelByUUID(r.Channel.UUID())
		channelRef = r.Channel.Reference()
		urn = r.URN
		break
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
