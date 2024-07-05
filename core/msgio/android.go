package msgio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"firebase.google.com/go/v4/messaging"
	fcm "github.com/appleboy/go-fcm"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type FCMClient interface {
	Send(ctx context.Context, message ...*messaging.Message) (*messaging.BatchResponse, error)
}

// SyncAndroidChannel tries to trigger sync of the given Android channel via FCM
func SyncAndroidChannel(ctx context.Context, fc FCMClient, channel *models.Channel) error {
	if fc == nil || reflect.ValueOf(fc).IsNil() {
		return errors.New("instance has no FCM configuration")
	}

	assert(channel.Type() == models.ChannelTypeAndroid, "can't sync a non-android channel")

	// no FCM ID for this channel, noop, we can't trigger a sync
	fcmID := channel.ConfigValue(models.ChannelConfigFCMID, "")
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

	if _, err := fc.Send(ctx, sync); err != nil {
		return fmt.Errorf("error syncing channel: %w", err)
	}

	slog.Debug("android sync complete", "elapsed", time.Since(start), "channel_uuid", channel.UUID())
	return nil
}

// CreateFCMClient creates an FCM client based on the configured FCM API key
func CreateFCMClient(ctx context.Context, cfg *runtime.Config) *fcm.Client {
	if cfg.AndroidFCMServiceAccountFile == "" {
		return nil
	}
	client, err := fcm.NewClient(ctx, fcm.WithCredentialsFile(cfg.AndroidFCMServiceAccountFile))
	if err != nil {
		panic(fmt.Errorf("unable to create FCM client: %w", err))
	}
	return client
}
