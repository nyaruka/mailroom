package channels

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	tasks.RegisterCron("sync_android_channels", &SyncAndroidChannelsCron{})
}

type SyncAndroidChannelsCron struct {
	FCMClient msgio.FCMClient
}

func (s *SyncAndroidChannelsCron) AllInstances() bool {
	return true
}

func (s *SyncAndroidChannelsCron) Next(last time.Time) time.Time {
	return tasks.CronNext(last, time.Minute*10)

}

func (s *SyncAndroidChannelsCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {

	if s.FCMClient == nil {
		s.FCMClient = msgio.CreateFCMClient(ctx, rt.Config)
	}

	oldSeenAndroidChannels, err := models.GetAndroidChannelsToSync(ctx, rt.DB)
	if err != nil {
		return nil, fmt.Errorf("error loading old seen android channels: %w", err)
	}

	erroredCount := 0
	syncedCount := 0

	for _, channel := range oldSeenAndroidChannels {
		err := msgio.SyncAndroidChannel(ctx, s.FCMClient, &channel, "")
		if err != nil {
			slog.Error("error syncing messages", "error", err, "channel_uuid", channel.UUID())
			erroredCount += 1
		} else {
			syncedCount += 1
		}

	}

	return map[string]any{"synced": syncedCount, "errored": erroredCount}, nil

}
