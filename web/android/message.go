package android

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/mailroom/v26/core/android"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/android/message", web.JSONPayload(handleMessage))
}

// Creates a new incoming message from an Android relayer sync.
//
//	{
//	  "org_id": 1,
//	  "channel_id": 12,
//	  "phone": "+250788123123",
//	  "text": "Hello world",
//	  "received_on": "2021-01-01T12:00:00Z"
//	}
type messageRequest struct {
	OrgID      models.OrgID     `json:"org_id"       validate:"required"`
	ChannelID  models.ChannelID `json:"channel_id"   validate:"required"`
	Phone      string           `json:"phone"        validate:"required"`
	Text       string           `json:"text"         validate:"required"`
	ReceivedOn time.Time        `json:"received_on"  validate:"required"`
}

func handleMessage(ctx context.Context, rt *runtime.Runtime, r *messageRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	msgID, duplicate, err := android.CreateMessage(ctx, rt, oa, r.ChannelID, r.Phone, r.Text, r.ReceivedOn)
	if err != nil {
		return nil, 0, err
	}

	return map[string]any{"id": msgID, "duplicate": duplicate}, http.StatusOK, nil
}
