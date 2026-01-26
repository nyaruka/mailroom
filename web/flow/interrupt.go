package flow

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
	task := &tasks.InterruptFlow{FlowID: r.FlowID}
	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
		return nil, 0, fmt.Errorf("error queuing interrupt flow task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
