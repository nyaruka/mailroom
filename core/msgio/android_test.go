package msgio_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncAndroidChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	mockFCM := rt.FCM.(*testsuite.MockFCMClient)

	// create some Android channels
	testChannel1 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": ""})       // no FCM ID
	testChannel2 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"}) // invalid FCM ID
	testChannel3 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"}) // valid FCM ID

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	channel1 := oa.ChannelByID(testChannel1.ID)
	channel2 := oa.ChannelByID(testChannel2.ID)
	channel3 := oa.ChannelByID(testChannel3.ID)

	err = msgio.SyncAndroidChannel(ctx, rt, channel1)
	assert.NoError(t, err) // noop
	err = msgio.SyncAndroidChannel(ctx, rt, channel1)
	assert.NoError(t, err)
	err = msgio.SyncAndroidChannel(ctx, rt, channel2)
	assert.EqualError(t, err, "error syncing channel: 401 error: 401 Unauthorized")
	err = msgio.SyncAndroidChannel(ctx, rt, channel3)
	assert.NoError(t, err)

	// check that we try to sync the 2 channels with FCM IDs, even tho one fails
	assert.Equal(t, 2, len(mockFCM.Messages))
	assert.Equal(t, "FCMID2", mockFCM.Messages[0].Token)
	assert.Equal(t, "FCMID3", mockFCM.Messages[1].Token)

	assert.Equal(t, "high", mockFCM.Messages[0].Android.Priority)
	assert.Equal(t, "sync", mockFCM.Messages[0].Android.CollapseKey)
	assert.Equal(t, map[string]string{"msg": "sync"}, mockFCM.Messages[0].Data)
}
