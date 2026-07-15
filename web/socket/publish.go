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
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
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

// the only client publication type accepted for now
const typeTypingStarted = "typing_started"

// the publication type fanned out to other subscribers
const typeAgentTyping = "agent_typing"

// Handles a client publication proxied by the centrifugo server. The connection meta is stamped server-side at
// connect time and is what publications are authorized against. Domain denials are returned in-body with HTTP 200
// as the proxy protocol requires.
//
//	{
//	  "client": "9336a229-2400-4382-8d27-9ec18b28219c",
//	  "user": "3",
//	  "channel": "history:a393abc0-283d-4c9b-a1b3-641a035c34bf",
//	  "data": {"type": "typing_started", "channel_uuid": "0f66...", "urn": "facebook:12345", "msg_external_id": "ex123"},
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

// what agent clients publish to a contact's history socket - the routing fields tell us where the contact last
// wrote from so the typing indicator can be forwarded to the right place
type typingData struct {
	Type          string             `json:"type"`
	ChannelUUID   assets.ChannelUUID `json:"channel_uuid"`
	URN           urns.URN           `json:"urn"`
	MsgExternalID string             `json:"msg_external_id"`
}

// the rewritten publication other subscribers receive - routing fields stripped, typist attributed
type agentTypingData struct {
	Type string                `json:"type"`
	User *assets.UserReference `json:"user"`
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
	if len(parts) != 2 || parts[0] != models.SocketHistoryNamespace {
		return deny("publishing not supported on this socket")
	}
	contactUUID := core.ContactUUID(parts[1])

	data := &typingData{}
	if err := json.Unmarshal(r.Data, data); err != nil {
		return deny("invalid publication data")
	}
	if data.Type != typeTypingStarted {
		return deny(fmt.Sprintf("unsupported publication type: %s", data.Type))
	}

	oa, err := models.GetOrgAssets(ctx, rt, r.Meta.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	// check that the contact whose socket this is and the chat channel both belong to the publisher's org
	contactIDs, err := models.GetContactIDsFromUUIDs(ctx, rt.DB, oa.OrgID(), []core.ContactUUID{contactUUID})
	if err != nil {
		return nil, 0, fmt.Errorf("error looking up contact: %w", err)
	}
	if len(contactIDs) == 0 {
		return deny("no such contact")
	}
	channel := oa.ChannelByUUID(data.ChannelUUID)
	if channel == nil {
		return deny("no such channel")
	}

	// attribute the typist - the publisher is usually a workspace user but might not be (e.g. staff)
	var typist *assets.UserReference
	if user := oa.UserByID(r.Meta.UserID); user != nil {
		typist = user.Reference()
	} else if user, err := models.LoadUser(ctx, rt.DB, r.Meta.UserID); err != nil {
		return nil, 0, fmt.Errorf("error loading user: %w", err)
	} else if user != nil {
		typist = user.Reference()
	} else {
		typist = assets.NewUserReference(r.Meta.UserUUID, "")
	}

	// forward to courier best effort - agent-to-agent co-presence is valid even if the platform call fails, and
	// courier throttles platform sends per conversation itself so every pulse is forwarded as-is
	fwdCtx, cancel := context.WithTimeout(ctx, courierTimeout)
	defer cancel()
	if _, err := courier.SendChatAction(fwdCtx, rt, channel, courier.ChatActionTypingStarted, data.URN, data.MsgExternalID); err != nil {
		slog.Error("error sending chat action to courier", "error", err, "channel", channel.UUID())
	}

	// unlike everything else published to history sockets this is ephemeral and never persisted, hence skip_history
	return &publishResponse{
		Result: &publishResult{Data: &agentTypingData{Type: typeAgentTyping, User: typist}, SkipHistory: true},
	}, http.StatusOK, nil
}
