package campaigns

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/redisx"
)

// TypeScheduleCampaignEvent is the type of the schedule event task
const TypeScheduleCampaignEvent = "schedule_campaign_event"

const scheduleLockKey string = "lock:schedule_campaign_event_%d"

func init() {
	tasks.RegisterType(TypeScheduleCampaignEvent, func() tasks.Task { return &ScheduleCampaignEventTask{} })
}

// ScheduleCampaignEventTask is our definition of our event recalculation task
type ScheduleCampaignEventTask struct {
	CampaignEventID models.CampaignEventID `json:"campaign_event_id"`
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

// Perform creates the actual event fires to schedule the given campaign event
func (t *ScheduleCampaignEventTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	locker := redisx.NewLocker(fmt.Sprintf(scheduleLockKey, t.CampaignEventID), time.Hour)
	lock, err := locker.Grab(ctx, rt.RP, time.Minute*5)
	if err != nil {
		return fmt.Errorf("error grabbing lock to schedule campaign event %d: %w", t.CampaignEventID, err)
	}
	defer locker.Release(ctx, rt.RP, lock)

	err = models.ScheduleCampaignEvent(ctx, rt, oa, t.CampaignEventID)
	if err != nil {
		return fmt.Errorf("error scheduling campaign event %d: %w", t.CampaignEventID, err)
	}

	return nil
}
