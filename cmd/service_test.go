package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/appleboy/go-fcm"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFCMCredentials(t *testing.T) {
	ctx := context.Background()

	// no credentials configured means no FCM client
	cfg := runtime.NewDefaultConfig()
	assert.Nil(t, fcmCredentials(cfg))

	// credentials given inline are enough to construct a client
	creds, err := os.ReadFile("../core/msgio/testdata/android.json")
	require.NoError(t, err)

	cfg = runtime.NewDefaultConfig()
	cfg.AndroidCredentials = string(creds)
	client, err := fcm.NewClient(ctx, fcmCredentials(cfg))
	assert.NoError(t, err)
	assert.NotNil(t, client)

	// as are credentials read from a file
	cfg = runtime.NewDefaultConfig()
	cfg.AndroidCredentialsFile = "../core/msgio/testdata/android.json"
	client, err = fcm.NewClient(ctx, fcmCredentials(cfg))
	assert.NoError(t, err)
	assert.NotNil(t, client)

	// a file that doesn't exist is an error at construction rather than first use
	cfg = runtime.NewDefaultConfig()
	cfg.AndroidCredentialsFile = "nope.json"
	_, err = fcm.NewClient(ctx, fcmCredentials(cfg))
	assert.ErrorContains(t, err, "cannot read credentials file")
}
