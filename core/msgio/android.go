package msgio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// SyncAndroidChannel tries to trigger sync of the given Android channel via FCM
func SyncAndroidChannel(ctx context.Context, rt *runtime.Runtime, channel *models.Channel) error {
	if rt.FCM == nil {
		return errors.New("instance has no FCM configuration")
	}

	assert(channel.IsAndroid(), "can't sync a non-android channel")

	// no FCM ID for this channel, noop, we can't trigger a sync
	fcmID := channel.Config().GetString(models.ChannelConfigFCMID, "")
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

	if _, err := rt.FCM.Send(ctx, sync); err != nil {
		VerifyFCMID(ctx, rt, channel, fcmID)

		return fmt.Errorf("error syncing channel: %w", err)
	}

	slog.Debug("android sync complete", "elapsed", time.Since(start), "channel_uuid", channel.UUID())
	return nil
}

func VerifyFCMID(ctx context.Context, rt *runtime.Runtime, channel *models.Channel, fcmID string) error {
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(rt.Config.AndroidCredentialsFile))
	if err != nil {
		return err
	}

	firebaseAuthClient, err := app.Auth(ctx)
	if err != nil {
		return err
	}
	// verify the FCM ID
	_, err = firebaseAuthClient.VerifyIDToken(ctx, fcmID)
	if err != nil {
		if auth.IsIDTokenRevoked(err) || auth.IsUserDisabled(err) {
			// clear the FCM ID in the DB
			_, errDB := rt.DB.ExecContext(ctx, `UPDATE channels_channel SET config = config || '{"FCM_ID": ""}'::jsonb WHERE uuid = $1`, channel.UUID())
			if errDB != nil {
				return errDB
			}
		}

		return err
	}
	return nil
}
