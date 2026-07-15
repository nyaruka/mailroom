package courier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// ChatAction is an ephemeral action performed against a chat, e.g. a typing indicator. Unlike message sends these
// are fire and forget - they aren't queued and there are no statuses or retries.
type ChatAction string

const (
	// ChatActionTypingStarted shows a typing indicator to the contact
	ChatActionTypingStarted ChatAction = "typing_started"

	// ChatActionTypingStopped clears a typing indicator
	ChatActionTypingStopped ChatAction = "typing_stopped"

	// ChatActionMarkRead shows the contact that their messages have been read
	ChatActionMarkRead ChatAction = "mark_read"
)

type sendChatActionRequest struct {
	Action        ChatAction         `json:"action"`
	ChannelType   models.ChannelType `json:"channel_type"`
	ChannelUUID   assets.ChannelUUID `json:"channel_uuid"`
	URN           urns.URN           `json:"urn"`
	MsgExternalID string             `json:"msg_external_id,omitempty"`
}

// ChatActionResponse is courier's response to a chat action send.
type ChatActionResponse struct {
	Supported bool `json:"supported"`
	Interval  int  `json:"interval,omitempty"` // seconds until the action should be resent to sustain it
}

// SendChatAction calls courier to send an ephemeral chat action to the given URN over the given channel. Courier
// throttles sustained actions per conversation to the platform's own interval, so callers can send at whatever
// cadence suits them. MsgExternalID is the platform's own identifier for the newest incoming message and is
// required by channels whose actions reference a message, e.g. WhatsApp.
func SendChatAction(ctx context.Context, rt *runtime.Runtime, ch *models.Channel, action ChatAction, urn urns.URN, msgExternalID string) (*ChatActionResponse, error) {
	payload := jsonx.MustMarshal(&sendChatActionRequest{
		Action:        action,
		ChannelType:   ch.Type(),
		ChannelUUID:   ch.UUID(),
		URN:           urn,
		MsgExternalID: msgExternalID,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(rt.Config.CourierEndpoint, "/")+"/ci/chat_action/send", bytes.NewReader(payload))
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", rt.Config.CourierAuthToken))

	// this is an internal request to courier whose trace we don't persist, so a plain fetch is enough
	resp, err := rt.HTTP.Services.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error calling courier endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading courier response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error calling courier endpoint, got non-200 status: %s", string(body))
	}
	ca := &ChatActionResponse{}
	if err := json.Unmarshal(body, ca); err != nil {
		return nil, fmt.Errorf("error unmarshaling courier response: %w", err)
	}

	return ca, nil
}
