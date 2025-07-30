package models_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannels(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetAll)

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
		features           []assets.ChannelFeature
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
			[]assets.ChannelFeature{},
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
			[]assets.ChannelFeature{},
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
			[]assets.ChannelFeature{"optins"},
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
			[]assets.ChannelFeature{},
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
		assert.Equal(t, tc.features, channel.Features())
		assert.Equal(t, tc.schemes, channel.Schemes())
		assert.Equal(t, tc.prefixes, channel.MatchPrefixes())
		assert.Equal(t, tc.allowInternational, channel.AllowInternational())
	}
}

func TestGetChannelByID(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetAll)

	ch, err := models.GetChannelByID(ctx, rt.DB.DB, testdb.TwilioChannel.ID)
	assert.NoError(t, err)
	assert.Equal(t, testdb.TwilioChannel.ID, ch.ID())
	assert.Equal(t, testdb.TwilioChannel.UUID, ch.UUID())

	_, err = models.GetChannelByID(ctx, rt.DB.DB, 1234567890)
	assert.EqualError(t, err, "error fetching channel by id 1234567890: error scanning row JSON: sql: no rows in result set")

}

func TestGetAndroidChannelsToSync(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, testsuite.ResetData)

	testChannel1 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{"FCM_ID": ""})
	testChannel2 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 2", "234", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID2"})
	testChannel3 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 3", "456", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID3"})
	testChannel4 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 4", "567", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID4"})
	testChannel5 := testdb.InsertChannel(rt, testdb.Org1, "A", "Android 5", "678", []string{"tel"}, "SR", map[string]any{"FCM_ID": "FCMID5"})

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
