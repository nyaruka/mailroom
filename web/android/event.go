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
	"github.com/nyaruka/null/v3"
)

func init() {
	web.InternalRoute(http.MethodPost, "/android/event", web.JSONPayload(handleEvent))
}

// Creates a new channel event from an Android relayer sync.
//
//	{
//	  "org_id": 1,
//	  "channel_id": 12,
//	  "phone": "+250788123123",
//	  "event_type": "mo_miss",
//	  "extra": {"duration": 3},
//	  "occurred_on": "2021-01-01T12:00:00Z"
//	}
type eventRequest struct {
	OrgID      models.OrgID            `json:"org_id"       validate:"required"`
	ChannelID  models.ChannelID        `json:"channel_id"   validate:"required"`
	Phone      string                  `json:"phone"        validate:"required"`
	EventType  models.ChannelEventType `json:"event_type"   validate:"required"`
	Extra      null.Map[any]           `json:"extra"        validate:"required"`
	OccurredOn time.Time               `json:"occurred_on"  validate:"required"`
}

func handleEvent(ctx context.Context, rt *runtime.Runtime, r *eventRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	e, err := android.CreateEvent(ctx, rt, oa, r.ChannelID, r.Phone, r.EventType, r.Extra, r.OccurredOn)
	if err != nil {
		return nil, 0, err
	}

	return map[string]any{"id": e.ID}, http.StatusOK, nil
}
