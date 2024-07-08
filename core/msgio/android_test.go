package msgio_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncAndroidChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	mockFirebase := testsuite.NewMockFirebaseService("FCMID3")
	fcmClient := mockFirebase.GetFirebaseCloudMessagingClient(ctx)
	rt.FirebaseCloudMessagingClient = fcmClient

	// create some Android channels
	testChannel1 := testdata.InsertChannel(rt, testdata.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": ""})       // no FCM ID
	testChannel2 := testdata.InsertChannel(rt, testdata.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"}) // invalid FCM ID
	testChannel3 := testdata.InsertChannel(rt, testdata.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"}) // valid FCM ID

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	channel1 := oa.ChannelByID(testChannel1.ID)
	channel2 := oa.ChannelByID(testChannel2.ID)
	channel3 := oa.ChannelByID(testChannel3.ID)

	rt.FirebaseCloudMessagingClient = nil

	err = msgio.SyncAndroidChannel(ctx, rt, channel1, "")
	assert.EqualError(t, err, "instance has no FCM configuration")

	rt.FirebaseCloudMessagingClient = fcmClient

	err = msgio.SyncAndroidChannel(ctx, rt, channel1, "")
	assert.NoError(t, err)
	err = msgio.SyncAndroidChannel(ctx, rt, channel2, "")
	assert.EqualError(t, err, "error cloud messaging id verification: invalid token")
	err = msgio.SyncAndroidChannel(ctx, rt, channel3, "")
	assert.NoError(t, err)

	// check that we try to sync the 2 channels with FCM IDs, even tho one fails
	assert.Equal(t, 2, len(mockFirebase.Messages))
	assert.Equal(t, "FCMID2", mockFirebase.Messages[0].Token)
	assert.Equal(t, "FCMID3", mockFirebase.Messages[1].Token)

	assert.Equal(t, "high", mockFirebase.Messages[0].Android.Priority)
	assert.Equal(t, "sync", mockFirebase.Messages[0].Android.CollapseKey)
	assert.Equal(t, map[string]string{"msg": "sync"}, mockFirebase.Messages[0].Data)

	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	channel2 = oa.ChannelByID(testChannel2.ID)
	assert.Equal(t, "", channel2.ConfigValue(models.ChannelConfigFCMID, ""))
}
