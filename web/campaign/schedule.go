package campaign

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/campaign/schedule", web.JSONPayload(handleSchedule))
}

// Request to schedule a campaign point. Triggers a background task to create the fires and returns immediately.
//
//	{
//	  "org_id": 1,
//	  "point_id": 123456
//	}
type scheduleRequest struct {
	OrgID   models.OrgID   `json:"org_id"   validate:"required"`
	PointID models.PointID `json:"point_id"`
}

func handleSchedule(ctx context.Context, rt *runtime.Runtime, r *scheduleRequest) (any, int, error) {
	// we don't actual need the org assets in this function but load them to validate the org id is valid
	// and they'll probably still be cached by the time the task starts
	if _, err := models.GetOrgAssets(ctx, rt, r.OrgID); err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	task := &tasks.ScheduleCampaignPoint{PointID: r.PointID}

	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
		return nil, 0, fmt.Errorf("error queuing schedule campaign point task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
