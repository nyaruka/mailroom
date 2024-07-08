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

	mockFCM := testsuite.NewMockFCMService("FCMID3")
	fc := mockFCM.GetClient(ctx)

	// create some Android channels
	testChannel1 := testdata.InsertChannel(rt, testdata.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": ""})       // no FCM ID
	testChannel2 := testdata.InsertChannel(rt, testdata.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"}) // invalid FCM ID
	testChannel3 := testdata.InsertChannel(rt, testdata.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"}) // valid FCM ID

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	channel1 := oa.ChannelByID(testChannel1.ID)
	channel2 := oa.ChannelByID(testChannel2.ID)
	channel3 := oa.ChannelByID(testChannel3.ID)

	err = msgio.SyncAndroidChannel(ctx, nil, channel1, "")
	assert.EqualError(t, err, "instance has no FCM configuration")
	err = msgio.SyncAndroidChannel(ctx, fc, channel1, "")
	assert.NoError(t, err)
	err = msgio.SyncAndroidChannel(ctx, fc, channel2, "")
	assert.EqualError(t, err, "error syncing channel: 401 error: 401 Unauthorized")
	err = msgio.SyncAndroidChannel(ctx, fc, channel3, "")
	assert.NoError(t, err)

	// check that we try to sync the 2 channels with FCM IDs, even tho one fails
	assert.Equal(t, 2, len(mockFCM.Messages))
	assert.Equal(t, "FCMID2", mockFCM.Messages[0].Token)
	assert.Equal(t, "FCMID3", mockFCM.Messages[1].Token)

	assert.Equal(t, "high", mockFCM.Messages[0].Android.Priority)
	assert.Equal(t, "sync", mockFCM.Messages[0].Android.CollapseKey)
	assert.Equal(t, map[string]string{"msg": "sync"}, mockFCM.Messages[0].Data)
}

func TestCreateFCMClient(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	rt.Config.AndroidFCMServiceAccountFile = `testdata/android.json`

	assert.NotNil(t, msgio.CreateFCMClient(ctx, rt.Config))

	rt.Config.AndroidFCMServiceAccountFile = ""

	assert.Nil(t, msgio.CreateFCMClient(ctx, rt.Config))

}
