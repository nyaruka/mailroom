package android

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
)

// CreateEvent creates a channel event reported by an Android relayer, queueing it for handling if it needs it.
func CreateEvent(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channelID models.ChannelID, phone string, eventType models.ChannelEventType, extra null.Map[any], occurredOn time.Time) (*models.ChannelEvent, error) {
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
