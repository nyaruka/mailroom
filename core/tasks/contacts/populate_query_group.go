package contacts

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/vkutil/locks"
)

// TypePopulateQueryGroup is the type of the populate group task
const TypePopulateQueryGroup = "populate_dynamic_group"

const populateGroupLockKey string = "lock:pop_dyn_group_%d"

func init() {
	tasks.RegisterType(TypePopulateQueryGroup, func() tasks.Task { return &PopulateQueryGroupTask{} })
}

// PopulateQueryGroupTask is our task to populate the contacts for a dynamic group
type PopulateQueryGroupTask struct {
	GroupID models.GroupID `json:"group_id"`
	Query   string         `json:"query"`
}

func (t *PopulateQueryGroupTask) Type() string {
	return TypePopulateQueryGroup
}

// Timeout is the maximum amount of time the task can run for
func (t *PopulateQueryGroupTask) Timeout() time.Duration {
	return time.Hour
}

func (t *PopulateQueryGroupTask) WithAssets() models.Refresh {
	return models.RefreshGroups
}

// Perform figures out the membership for a query based group then repopulates it
func (t *PopulateQueryGroupTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	locker := locks.NewLocker(fmt.Sprintf(populateGroupLockKey, t.GroupID), time.Hour)
	lock, err := locker.Grab(ctx, rt.VK, time.Minute*5)
	if err != nil {
		return fmt.Errorf("error grabbing lock to repopulate query group: %d: %w", t.GroupID, err)
	}
	defer locker.Release(ctx, rt.VK, lock)

	start := time.Now()

	slog.Info("starting population of query group", "group_id", t.GroupID, "org_id", oa.OrgID(), "query", t.Query)

	count, err := search.PopulateGroup(ctx, rt, oa, t.GroupID, t.Query)
	if err != nil {
		return fmt.Errorf("error populating query group: %d: %w", t.GroupID, err)
	}
	slog.Info("completed populating query group", "elapsed", time.Since(start), "count", count)

	return nil
}
