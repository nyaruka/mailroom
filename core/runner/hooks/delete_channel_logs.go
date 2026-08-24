package hooks

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// DeleteChannelLogs is our hook for deleting the channel logs of deleted messages from DynamoDB
var DeleteChannelLogs runner.PostCommitHook = &deleteChannelLogs{}

type deleteChannelLogs struct{}

func (h *deleteChannelLogs) Order() int { return 20 }

func (h *deleteChannelLogs) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	msgUUIDs := make([]events.EventUUID, 0, len(scenes))
	for _, args := range scenes {
		for _, a := range args {
			msgUUIDs = append(msgUUIDs, a.(events.EventUUID))
		}
	}

	if err := models.DeleteChannelLogsForMessages(ctx, rt, oa.OrgID(), msgUUIDs); err != nil {
		slog.Error("error deleting channel logs of deleted messages", "error", err, "org_id", oa.OrgID(), "count", len(msgUUIDs))
	}

	return nil
}
