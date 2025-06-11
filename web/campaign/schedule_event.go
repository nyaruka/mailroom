package campaign

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/campaigns"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/campaign/schedule_event", web.RequireAuthToken(web.JSONPayload(handleScheduleEvent)))
}

// Request to schedule a campaign event. Triggers a background task to create the fires and returns immediately.
//
//	{
//	  "org_id": 1,
//	  "event_id": 123456
//	}
type scheduleEventRequest struct {
	OrgID   models.OrgID           `json:"org_id"   validate:"required"`
	EventID models.CampaignEventID `json:"event_id" validate:"required"`
}

func handleScheduleEvent(ctx context.Context, rt *runtime.Runtime, r *scheduleEventRequest) (any, int, error) {
	// dwe don't actual need the org assets in this function but load them to validate the org id is valid
	// and they'll probably still be cached by the time the task starts
	if _, err := models.GetOrgAssets(ctx, rt, r.OrgID); err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	task := &campaigns.ScheduleCampaignEventTask{CampaignEventID: r.EventID}

	rc := rt.RP.Get()
	defer rc.Close()
	if err := tasks.Queue(rc, tasks.BatchQueue, r.OrgID, task, true); err != nil {
		return nil, 0, fmt.Errorf("error queuing schedule campaign event task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
