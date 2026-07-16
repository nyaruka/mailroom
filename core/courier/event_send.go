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
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

type sendEventRequest struct {
	ChannelType models.ChannelType `json:"channel_type"`
	Event       events.Event       `json:"event"`
}

// SendEventResponse is courier's response to an event send.
type SendEventResponse struct {
	Supported bool `json:"supported"`
	Interval  int  `json:"interval,omitempty"` // seconds until the event should be resent to sustain its effect
}

// SendEvent calls courier to send an engine event, e.g. a user typing indicator, to the given channel's platform.
// The event's own channel/urn/msg_external_id fields say where it should go - the channel is passed to provide the
// type that courier looks channels up by. Event sends are fire and forget, and courier throttles sustained events
// per conversation to the platform's own interval, so callers can send at whatever cadence suits them.
func SendEvent(ctx context.Context, rt *runtime.Runtime, ch *models.Channel, event events.Event) (*SendEventResponse, error) {
	payload := jsonx.MustMarshal(&sendEventRequest{ChannelType: ch.Type(), Event: event})

	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(rt.Config.CourierEndpoint, "/")+"/ci/event/send", bytes.NewReader(payload))
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
	se := &SendEventResponse{}
	if err := json.Unmarshal(body, se); err != nil {
		return nil, fmt.Errorf("error unmarshaling courier response: %w", err)
	}

	return se, nil
}
