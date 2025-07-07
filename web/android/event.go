package android

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
	"github.com/nyaruka/null/v3"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/android/event", web.RequireAuthToken(web.JSONPayload(handleEvent)))
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
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	cu, err := resolveContact(ctx, rt, oa, r.ChannelID, r.Phone)
	if err != nil {
		return nil, 0, fmt.Errorf("error resolving contact: %w", err)
	}

	// only missed call events from Android relayers need handling, rest are just historical records
	needsHandling := r.EventType == models.EventTypeMissedCall

	status := models.EventStatusHandled
	if needsHandling {
		status = models.EventStatusPending
	}

	e := models.NewChannelEvent(r.OrgID, r.EventType, r.ChannelID, cu.contactID, cu.urnID, status, r.Extra, r.OccurredOn)
	if err := e.Insert(ctx, rt.DB); err != nil {
		return nil, 0, fmt.Errorf("error inserting event: %w", err)
	}

	if needsHandling {
		rc := rt.VK.Get()
		defer rc.Close()

		err = handler.QueueTask(ctx, rc, r.OrgID, e.ContactID, &ctasks.EventReceivedTask{
			EventID:    e.ID,
			EventType:  e.EventType,
			ChannelID:  e.ChannelID,
			URNID:      e.URNID,
			Extra:      e.Extra,
			CreatedOn:  e.CreatedOn,
			NewContact: cu.newContact,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("error queueing handle task: %w", err)
		}
	}

	return map[string]any{"id": e.ID}, http.StatusOK, nil
}
