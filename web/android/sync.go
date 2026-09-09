package android

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/msgio"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/android/sync", web.JSONPayload(handleSync))
}

type syncRequest struct {
	ChannelID models.ChannelID `json:"channel_id"   validate:"required"`
}

func handleSync(ctx context.Context, rt *runtime.Runtime, r *syncRequest) (any, int, error) {
	channel, err := models.GetChannelByID(ctx, rt.DB.DB, r.ChannelID)
	if err != nil {
		return nil, 0, fmt.Errorf("error resolving channel: %w", err)
	}

	// a channel that has never reported an FCM registration id (e.g. one that predates FCM) can't be nudged, and
	// SyncAndroidChannel treats that as a no-op rather than an error
	if err := msgio.SyncAndroidChannel(ctx, rt, channel); err != nil {
		return nil, 0, fmt.Errorf("error syncing android channel: %w", err)
	}

	return map[string]any{"id": channel.ID()}, http.StatusOK, nil
}
