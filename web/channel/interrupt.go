package channel

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/channel/interrupt", web.JSONPayload(handleInterrupt))
}

// Request that a channel is interrupted, because it's being deleted or its workspace suspended. The reason its queued
// messages are failed with defaults to the channel being removed.
//
//	{
//	  "org_id": 1,
//	  "channel_id": 235,
//	  "reason": "S"
//	}
type interruptRequest struct {
	OrgID     models.OrgID           `json:"org_id"     validate:"required"`
	ChannelID models.ChannelID       `json:"channel_id" validate:"required"`
	Reason    models.MsgFailedReason `json:"reason"     validate:"omitempty,oneof=R S"`
}

func handleInterrupt(ctx context.Context, rt *runtime.Runtime, r *interruptRequest) (any, int, error) {
	task := &tasks.InterruptChannel{ChannelID: r.ChannelID, Reason: r.Reason}
	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
		return nil, 0, fmt.Errorf("error queuing interrupt channel task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
