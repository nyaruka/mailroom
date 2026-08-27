package android

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
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

	e, err := createChannelEvent(ctx, rt, oa, r.ChannelID, r.Phone, r.EventType, r.Extra, r.OccurredOn)
	if err != nil {
		return nil, 0, err
	}

	return map[string]any{"id": e.ID}, http.StatusOK, nil
}

// createChannelEvent creates a channel event reported by an Android relayer, queueing it for handling if it needs it.
func createChannelEvent(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channelID models.ChannelID, phone string, eventType models.ChannelEventType, extra null.Map[any], occurredOn time.Time) (*models.ChannelEvent, error) {
	cu, err := resolveContact(ctx, rt, oa, channelID, phone)
	if err != nil {
		return nil, fmt.Errorf("error resolving contact: %w", err)
	}

	// only missed call events from Android relayers need handling, rest are just historical records
	needsHandling := eventType == models.EventTypeMissedCall

	status := models.EventStatusHandled
	if needsHandling {
		status = models.EventStatusPending
	}

	e := models.NewChannelEvent(oa.OrgID(), eventType, channelID, cu.contactID, cu.urnID, status, extra, occurredOn)
	if err := e.Insert(ctx, rt.DB); err != nil {
		return nil, fmt.Errorf("error inserting event: %w", err)
	}

	if needsHandling {
		err = tasks.QueueContact(ctx, rt, oa.OrgID(), e.ContactID, &ctasks.EventReceived{
			EventUUID:  e.UUID,
			EventType:  e.EventType,
			ChannelID:  e.ChannelID,
			URNID:      e.URNID,
			Extra:      e.Extra,
			NewContact: cu.newContact,
		})
		if err != nil {
			return nil, fmt.Errorf("error queueing handle task: %w", err)
		}
	}

	return e, nil
}
