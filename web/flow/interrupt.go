package flow

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
	"github.com/nyaruka/vkutil/locks"
)

func init() {
	web.InternalRoute(http.MethodPost, "/flow/interrupt", web.JSONPayload(handleInterrupt))
}

// Request that sessions using the given flow are interrupted. Used as part of flow archival.
//
//	{
//	  "org_id": 1,
//	  "flow_id": 235
//	}
type interruptRequest struct {
	OrgID  models.OrgID  `json:"org_id"  validate:"required"`
	FlowID models.FlowID `json:"flow_id" validate:"required"`
}

func handleInterrupt(ctx context.Context, rt *runtime.Runtime, r *interruptRequest) (any, int, error) {
	locker := locks.NewLocker(fmt.Sprintf("flow_interrupt:%d", r.FlowID), time.Second*30)
	lock, err := locker.Grab(ctx, rt.VK, time.Second*5)
	if err != nil {
		return nil, 0, fmt.Errorf("error grabbing lock for flow interruption: %w", err)
	}
	defer locker.Release(ctx, rt.VK, lock)

	// check if there is already an interruption in progress for this flow
	vc := rt.VK.Get()
	remaining, err := redis.Int(vc.Do("GET", fmt.Sprintf("%s:%d", "interrupt_flow_progress", r.FlowID)))
	vc.Close()
	if err != nil && err != redis.ErrNil {
		return nil, 0, fmt.Errorf("error checking flow interrupt progress: %w", err)
	}
	if remaining > 0 {
		return map[string]any{"interrupted": false}, http.StatusOK, nil
	}

	task := &tasks.InterruptFlow{FlowID: r.FlowID}
	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
		return nil, 0, fmt.Errorf("error queuing interrupt flow task: %w", err)
	}

	return map[string]any{"interrupted": true}, http.StatusOK, nil
}
