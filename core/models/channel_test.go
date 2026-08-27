package models_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannels(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// add some tel specific config to channel 2
	rt.DB.MustExec(`UPDATE channels_channel SET config = '{"matching_prefixes": ["250", "251"], "allow_international": true}' WHERE id = $1`, testdb.VonageChannel.ID)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, 1, models.RefreshChannels)
	require.NoError(t, err)

	channels, err := oa.Channels()
	require.NoError(t, err)

	tcs := []struct {
		id                 models.ChannelID
		uuid               assets.ChannelUUID
		name               string
		address            string
		schemes            []string
		roles              []assets.ChannelRole
		prefixes           []string
		allowInternational bool
	}{
		{
			testdb.TwilioChannel.ID,
			testdb.TwilioChannel.UUID,
			"Twilio",
			"+13605551212",
			[]string{"tel"},
			[]assets.ChannelRole{"send", "receive", "call", "answer"},
			nil,
			false,
		},
		{
			testdb.VonageChannel.ID,
			testdb.VonageChannel.UUID,
			"Vonage",
			"5789",
			[]string{"tel"},
			[]assets.ChannelRole{"send", "receive"},
			[]string{"250", "251"},
			true,
		},
		{
			testdb.FacebookChannel.ID,
			testdb.FacebookChannel.UUID,
			"Facebook",
			"12345",
			[]string{"facebook"},
			[]assets.ChannelRole{"send", "receive"},
			nil,
			false,
		},
		{
			testdb.AndroidChannel.ID,
			testdb.AndroidChannel.UUID,
			"Android",
			"+593123456789",
			[]string{"tel"},
			[]assets.ChannelRole{"send", "receive"},
			nil,
			false,
		},
	}

	assert.Equal(t, len(tcs), len(channels))
	for i, tc := range tcs {
		channel := channels[i].(*models.Channel)
		assert.Equal(t, tc.uuid, channel.UUID())
		assert.Equal(t, tc.id, channel.ID())
		assert.Equal(t, tc.name, channel.Name())
		assert.Equal(t, tc.address, channel.Address())
		assert.Equal(t, tc.roles, channel.Roles())
		assert.Equal(t, tc.schemes, channel.Schemes())
		assert.Equal(t, tc.prefixes, channel.MatchPrefixes())
		assert.Equal(t, tc.allowInternational, channel.AllowInternational())
	}
}

func TestGetChannelByID(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer rt.DB.MustExec(`UPDATE channels_channel SET is_active = TRUE WHERE id = $1`, testdb.VonageChannel.ID)

	ch, err := models.GetChannelByID(ctx, rt.DB.DB, testdb.TwilioChannel.ID)
	assert.NoError(t, err)
	assert.Equal(t, testdb.TwilioChannel.ID, ch.ID())
	assert.Equal(t, testdb.TwilioChannel.UUID, ch.UUID())

	// test when channel is deleted
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.VonageChannel.ID)

	ch, err = models.GetChannelByID(ctx, rt.DB.DB, testdb.VonageChannel.ID)
	assert.NoError(t, err)
	assert.Equal(t, testdb.VonageChannel.ID, ch.ID())
	assert.Equal(t, testdb.VonageChannel.UUID, ch.UUID())

	_, err = models.GetChannelByID(ctx, rt.DB.DB, 1234567890)
	assert.EqualError(t, err, "error fetching channel by id 1234567890: error scanning row JSON: sql: no rows in result set")

}

func TestGetAndroidChannelsToSync(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testChannel1 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": ""})
	testChannel2 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"})
	testChannel3 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"})
	testChannel4 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 4", "567", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID4"})
	testChannel5 := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Android 5", "678", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID5"})

	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '30 minutes' WHERE id = $1`, testChannel1.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '30 minutes' WHERE id = $1`, testChannel2.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() WHERE id = $1`, testChannel3.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '20 minutes' WHERE id = $1`, testChannel4.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NOW() - INTERVAL '10 days' WHERE id = $1`, testChannel5.ID)

	oldSeenAndroidChannels, err := models.GetAndroidChannelsToSync(ctx, rt.DB)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(oldSeenAndroidChannels))

	assert.Equal(t, testChannel4.ID, oldSeenAndroidChannels[0].ID())
	assert.Equal(t, testChannel2.ID, oldSeenAndroidChannels[1].ID())
	assert.Equal(t, testChannel1.ID, oldSeenAndroidChannels[2].ID())

}

func TestGetAndroidChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE channels_channel SET secret = 'sesame', claim_code = 'CLAIM1', device = 'Nexus', os = '7.0' WHERE id = $1`, testdb.AndroidChannel.ID)

	ch, err := models.GetAndroidChannel(ctx, rt.DB, testdb.AndroidChannel.ID)
	assert.NoError(t, err)
	assert.Equal(t, testdb.AndroidChannel.ID, ch.ID)
	assert.Equal(t, testdb.AndroidChannel.UUID, ch.UUID)
	assert.Equal(t, testdb.Org1.ID, ch.OrgID)
	assert.True(t, ch.IsActive)
	assert.Equal(t, "sesame", ch.Secret)
	assert.Equal(t, "CLAIM1", ch.ClaimCode)
	assert.Equal(t, "Nexus", ch.Device)
	assert.Equal(t, "7.0", ch.OS)

	// released channels are still returned, because their relayer needs telling to stop
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.AndroidChannel.ID)
	ch, err = models.GetAndroidChannel(ctx, rt.DB, testdb.AndroidChannel.ID)
	assert.NoError(t, err)
	assert.False(t, ch.IsActive)

	// but channels that aren't android, or don't exist, aren't
	_, err = models.GetAndroidChannel(ctx, rt.DB, testdb.TwilioChannel.ID)
	assert.ErrorIs(t, err, models.ErrNotFound)

	_, err = models.GetAndroidChannel(ctx, rt.DB, models.ChannelID(1234567))
	assert.ErrorIs(t, err, models.ErrNotFound)
}

func TestUpdateAndroidChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE channels_channel SET last_seen = NULL WHERE id = $1`, testdb.AndroidChannel.ID)

	err := models.UpdateAndroidChannelSeen(ctx, rt.DB, testdb.AndroidChannel.ID)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM channels_channel WHERE id = $1 AND last_seen > NOW() - INTERVAL '1 minute'`, testdb.AndroidChannel.ID).Returns(1)

	newUUID := assets.ChannelUUID("f3d5ccd0-fee0-4955-bcb7-21d9f1b0d5f1")
	err = models.UpdateAndroidChannelApp(ctx, rt.DB, testdb.AndroidChannel.ID, "FCM123", newUUID)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT config->>'FCM_ID', uuid::text FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).
		Columns(map[string]any{"?column?": "FCM123", "uuid": string(newUUID)})

	// an older relayer doesn't report a uuid, and mustn't be allowed to clear the one we have
	err = models.UpdateAndroidChannelApp(ctx, rt.DB, testdb.AndroidChannel.ID, "FCM456", "")
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT uuid::text FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).Returns(string(newUUID))

	err = models.UpdateAndroidChannelDevice(ctx, rt.DB, testdb.AndroidChannel.ID, "Pixel", "13")
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT device, os FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).
		Columns(map[string]any{"device": "Pixel", "os": "13"})
}

func TestReleaseAndroidChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testdb.InsertIncomingCallTrigger(t, rt, testdb.Org1, testdb.Favorites, nil, nil, testdb.AndroidChannel)
	rt.DB.MustExec(`INSERT INTO notifications_incident(org_id, incident_type, scope, started_on, channel_id) VALUES($1, 'channel:outdated_app', $2, NOW(), $3)`, testdb.Org1.ID, fmt.Sprint(testdb.AndroidChannel.ID), testdb.AndroidChannel.ID)

	// a trigger on a different channel that shouldn't be touched
	testdb.InsertIncomingCallTrigger(t, rt, testdb.Org1, testdb.Favorites, nil, nil, testdb.TwilioChannel)

	ch, err := models.GetAndroidChannel(ctx, rt.DB, testdb.AndroidChannel.ID)
	require.NoError(t, err)

	assert.NoError(t, models.ReleaseAndroidChannel(ctx, rt.DB, ch))

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM channels_channel WHERE id = $1 AND is_active = FALSE`, testdb.AndroidChannel.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM triggers_trigger WHERE channel_id = $1 AND is_active = FALSE AND is_archived = TRUE`, testdb.AndroidChannel.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE channel_id = $1 AND ended_on IS NULL`, testdb.AndroidChannel.ID).Returns(0)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM triggers_trigger WHERE channel_id = $1 AND is_active = TRUE`, testdb.TwilioChannel.ID).Returns(1)
}
