package msgio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"firebase.google.com/go/v4/messaging"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type FCMClient interface {
	Send(ctx context.Context, message *messaging.Message) (string, error)
}

// SyncAndroidChannel tries to trigger sync of the given Android channel via FCM
func SyncAndroidChannel(ctx context.Context, rt *runtime.Runtime, channel *models.Channel, registrationID string) error {
	if rt.FirebaseCloudMessagingClient == nil {
		return errors.New("instance has no FCM configuration")
	}

	assert(channel.IsAndroid(), "can't sync a non-android channel")

	// no FCM ID for this channel, noop, we can't trigger a sync
	fcmID := channel.ConfigValue(models.ChannelConfigFCMID, "")
	if registrationID != "" {
		fcmID = registrationID
	}
	if fcmID == "" {
		return nil
	}

	sync := &messaging.Message{
		Token: fcmID,
		Android: &messaging.AndroidConfig{
			Priority:    "high",
			CollapseKey: "sync",
		},
		Data: map[string]string{"msg": "sync"},
	}

	start := time.Now()

	if _, err := rt.FirebaseCloudMessagingClient.Send(ctx, sync); err != nil {
		// verify the FCM ID
		_, err = rt.FirebaseAuthClient.VerifyIDToken(ctx, fcmID)
		if err != nil {
			// clear the FCM ID in the DB
			rt.DB.ExecContext(ctx, `UPDATE channels_channel SET config = config || '{"FCM_ID": ""}'::jsonb WHERE uuid = $1`, channel.UUID())
			slog.Debug("android cloud messaging id verification failed", "channel_uuid", channel.UUID())
			return fmt.Errorf("error cloud messaging id verification: %w", err)
		}

		return fmt.Errorf("error syncing channel: %w", err)
	}

	slog.Debug("android sync complete", "elapsed", time.Since(start), "channel_uuid", channel.UUID())
	return nil
}
