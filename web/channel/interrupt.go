package channel

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/interrupts"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/channel/interrupt", web.RequireAuthToken(web.JSONPayload(handleInterrupt)))
}

// Request that a channel is interrupted. Used as part of channel deletion.
//
//	{
//	  "org_id": 1,
//	  "channel_id": 235
//	}
type interruptRequest struct {
	OrgID     models.OrgID     `json:"org_id"     validate:"required"`
	ChannelID models.ChannelID `json:"channel_id" validate:"required"`
}

func handleInterrupt(ctx context.Context, rt *runtime.Runtime, r *interruptRequest) (any, int, error) {
	task := &interrupts.InterruptChannelTask{ChannelID: r.ChannelID}
	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
		return nil, 0, fmt.Errorf("error queuing start flow task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
