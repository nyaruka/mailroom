package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/vkutil/locks"
)

// TypeScheduleCampaignPoint is the type of the schedule campaign point task
const TypeScheduleCampaignPoint = "schedule_campaign_point"

const scheduleLockKey string = "schedule_campaign_point_%d"

func init() {
	RegisterType(TypeScheduleCampaignPoint, func() Task { return &ScheduleCampaignPoint{} })
}

// ScheduleCampaignPoint is our definition of our event recalculation task
type ScheduleCampaignPoint struct {
	PointID models.PointID `json:"point_id"`
}

func (t *ScheduleCampaignPoint) Type() string {
	return TypeScheduleCampaignPoint
}

// Timeout is the maximum amount of time the task can run for
func (t *ScheduleCampaignPoint) Timeout() time.Duration {
	return time.Hour
}

func (t *ScheduleCampaignPoint) WithAssets() models.Refresh {
	return models.RefreshCampaigns | models.RefreshFields
}

// Perform creates the actual event fires to schedule the given campaign point
func (t *ScheduleCampaignPoint) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	locker := locks.NewLocker(fmt.Sprintf(scheduleLockKey, t.PointID), time.Hour)
	lock, err := locker.Grab(ctx, rt.VK, time.Minute*5)
	if err != nil {
		return fmt.Errorf("error grabbing lock to schedule campaign point %d: %w", t.PointID, err)
	}
	defer locker.Release(ctx, rt.VK, lock)

	err = models.ScheduleCampaignPoint(ctx, rt, oa, t.PointID)
	if err != nil {
		return fmt.Errorf("error scheduling campaign point %d: %w", t.PointID, err)
	}

	return nil
}
