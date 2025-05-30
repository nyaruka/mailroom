package crons_test

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestSyncAndroidChannels(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	rt.Config.AndroidCredentialsFile = `testdata/android.json`

	defer testsuite.Reset(testsuite.ResetData)

	testChannel1 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": ""})       // no FCM ID
	testChannel2 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"}) // invalid FCM ID
	testChannel3 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"}) // valid FCM ID
	testChannel4 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 4", "567", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID4"}) // valid FCM ID
	testChannel5 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 5", "678", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID5"}) // valid FCM ID

	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '30 minutes' WHERE id = $1`, testChannel1.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '30 minutes' WHERE id = $1`, testChannel2.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() WHERE id = $1`, testChannel3.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '20 minutes' WHERE id = $1`, testChannel4.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '10 days' WHERE id = $1`, testChannel5.ID)

	time.Sleep(5 * time.Millisecond)

	cron := &crons.SyncAndroidChannelsCron{}
	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"synced": 2, "errored": 1}, res)

}
