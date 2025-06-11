package android

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/android/sync", web.RequireAuthToken(web.JSONPayload(handleSync)))
}

type syncRequest struct {
	ChannelID models.ChannelID `json:"channel_id"   validate:"required"`
}

func handleSync(ctx context.Context, rt *runtime.Runtime, r *syncRequest) (any, int, error) {
	channel, err := models.GetChannelByID(ctx, rt.DB.DB, r.ChannelID)
	if err != nil {
		return nil, 0, fmt.Errorf("error resolving channel: %w", err)
	}

	channelFCMID := channel.Config().GetString(models.ChannelConfigFCMID, "")
	if channelFCMID == "" {
		return nil, 0, fmt.Errorf("missing android channel registration id")
	}

	err = msgio.SyncAndroidChannel(ctx, rt, channel)
	if err != nil {
		return nil, 0, fmt.Errorf("error syncing android channel: %w", err)
	}

	return map[string]any{"id": channel.ID()}, http.StatusOK, nil
}
