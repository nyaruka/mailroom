package campaigns

import (
	"context"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeScheduleCampaignEvent = "schedule_campaign_event"

func init() {
	tasks.RegisterType(TypeScheduleCampaignEvent, func() tasks.Task { return &ScheduleCampaignEventTask{} })
}

type ScheduleCampaignEventTask struct {
	EventID models.PointID `json:"campaign_event_id"`
}

func (t *ScheduleCampaignEventTask) Type() string {
	return TypeScheduleCampaignEvent
}

// Timeout is the maximum amount of time the task can run for
func (t *ScheduleCampaignEventTask) Timeout() time.Duration {
	return time.Hour
}

func (t *ScheduleCampaignEventTask) WithAssets() models.Refresh {
	return models.RefreshCampaigns | models.RefreshFields
}

// Perform creates the actual event fires to schedule the given campaign point
func (t *ScheduleCampaignEventTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	return (&ScheduleCampaignPointTask{PointID: t.EventID}).Perform(ctx, rt, oa)
}
