package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadTriggers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	rt.DB.MustExec(`DELETE FROM triggers_trigger`)
	farmersGroup := testdb.InsertContactGroup(rt, testdb.Org1, assets.GroupUUID(uuids.NewV4()), "Farmers", "")

	// create trigger for other org to ensure it isn't loaded
	testdb.InsertCatchallTrigger(rt, testdb.Org2, testdb.Org2Favorites, nil, nil, nil)

	tcs := []struct {
		id               models.TriggerID
		type_            models.TriggerType
		flowID           models.FlowID
		keywords         []string
		keywordMatchType models.MatchType
		referrerID       string
		includeGroups    []models.GroupID
		excludeGroups    []models.GroupID
		includeContacts  []models.ContactID
		channelID        models.ChannelID
	}{
		{
			id:               testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"join"}, models.MatchFirst, nil, nil, nil),
			type_:            models.KeywordTriggerType,
			flowID:           testdb.Favorites.ID,
			keywords:         []string{"join"},
			keywordMatchType: models.MatchFirst,
		},
		{
			id:               testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"join"}, models.MatchFirst, nil, nil, testdb.TwilioChannel),
			type_:            models.KeywordTriggerType,
			flowID:           testdb.Favorites.ID,
			keywords:         []string{"join"},
			keywordMatchType: models.MatchFirst,
			channelID:        testdb.TwilioChannel.ID,
		},
		{
			id:               testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.PickANumber, []string{"start"}, models.MatchOnly, []*testdb.Group{testdb.DoctorsGroup, testdb.TestersGroup}, []*testdb.Group{farmersGroup}, nil),
			type_:            models.KeywordTriggerType,
			flowID:           testdb.PickANumber.ID,
			keywords:         []string{"start"},
			keywordMatchType: models.MatchOnly,
			includeGroups:    []models.GroupID{testdb.DoctorsGroup.ID, testdb.TestersGroup.ID},
			excludeGroups:    []models.GroupID{farmersGroup.ID},
		},
		{
			id:            testdb.InsertIncomingCallTrigger(rt, testdb.Org1, testdb.Favorites, []*testdb.Group{testdb.DoctorsGroup, testdb.TestersGroup}, []*testdb.Group{farmersGroup}, nil),
			type_:         models.IncomingCallTriggerType,
			flowID:        testdb.Favorites.ID,
			includeGroups: []models.GroupID{testdb.DoctorsGroup.ID, testdb.TestersGroup.ID},
			excludeGroups: []models.GroupID{farmersGroup.ID},
		},
		{
			id:     testdb.InsertIncomingCallTrigger(rt, testdb.Org1, testdb.Favorites, []*testdb.Group{testdb.DoctorsGroup, testdb.TestersGroup}, []*testdb.Group{farmersGroup}, testdb.TwilioChannel),
			type_:  models.IncomingCallTriggerType,
			flowID: testdb.Favorites.ID,

			includeGroups: []models.GroupID{testdb.DoctorsGroup.ID, testdb.TestersGroup.ID},
			excludeGroups: []models.GroupID{farmersGroup.ID},
			channelID:     testdb.TwilioChannel.ID,
		},
		{
			id:     testdb.InsertMissedCallTrigger(rt, testdb.Org1, testdb.Favorites, nil),
			type_:  models.MissedCallTriggerType,
			flowID: testdb.Favorites.ID,
		},
		{
			id:        testdb.InsertNewConversationTrigger(rt, testdb.Org1, testdb.Favorites, testdb.TwilioChannel),
			type_:     models.NewConversationTriggerType,
			flowID:    testdb.Favorites.ID,
			channelID: testdb.TwilioChannel.ID,
		},
		{
			id:     testdb.InsertReferralTrigger(rt, testdb.Org1, testdb.Favorites, "", nil),
			type_:  models.ReferralTriggerType,
			flowID: testdb.Favorites.ID,
		},
		{
			id:         testdb.InsertReferralTrigger(rt, testdb.Org1, testdb.Favorites, "3256437635", testdb.TwilioChannel),
			type_:      models.ReferralTriggerType,
			flowID:     testdb.Favorites.ID,
			referrerID: "3256437635",
			channelID:  testdb.TwilioChannel.ID,
		},
		{
			id:     testdb.InsertCatchallTrigger(rt, testdb.Org1, testdb.Favorites, nil, nil, nil),
			type_:  models.CatchallTriggerType,
			flowID: testdb.Favorites.ID,
		},
		{
			id:        testdb.InsertCatchallTrigger(rt, testdb.Org1, testdb.Favorites, nil, nil, testdb.TwilioChannel),
			type_:     models.CatchallTriggerType,
			flowID:    testdb.Favorites.ID,
			channelID: testdb.TwilioChannel.ID,
		},
	}

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	require.Equal(t, len(tcs), len(oa.Triggers()))

	for i, tc := range tcs {
		actual := oa.Triggers()[i]

		assert.Equal(t, tc.id, actual.ID(), "id mismatch in trigger #%d", i)
		assert.Equal(t, tc.type_, actual.TriggerType(), "type mismatch in trigger #%d", i)
		assert.Equal(t, tc.flowID, actual.FlowID(), "flow id mismatch in trigger #%d", i)
		assert.Equal(t, tc.keywords, actual.Keywords(), "keywords mismatch in trigger #%d", i)
		assert.Equal(t, tc.keywordMatchType, actual.MatchType(), "match type mismatch in trigger #%d", i)
		assert.Equal(t, tc.referrerID, actual.ReferrerID(), "referrer id mismatch in trigger #%d", i)
		assert.ElementsMatch(t, tc.includeGroups, actual.IncludeGroupIDs(), "include groups mismatch in trigger #%d", i)
		assert.ElementsMatch(t, tc.excludeGroups, actual.ExcludeGroupIDs(), "exclude groups mismatch in trigger #%d", i)
		assert.ElementsMatch(t, tc.includeContacts, actual.ContactIDs(), "include contacts mismatch in trigger #%d", i)
		assert.Equal(t, tc.channelID, actual.ChannelID(), "channel id mismatch in trigger #%d", i)
	}
}

func TestFindMatchingMsgTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	rt.DB.MustExec(`DELETE FROM triggers_trigger`)

	joinID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"join"}, models.MatchFirst, nil, nil, nil)
	joinTwilioOnlyID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"join"}, models.MatchFirst, nil, nil, testdb.TwilioChannel)
	startTwilioOnlyID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"start"}, models.MatchFirst, nil, nil, testdb.TwilioChannel)
	resistID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.SingleMessage, []string{"resist"}, models.MatchOnly, nil, nil, nil)
	resistTwilioOnlyID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.SingleMessage, []string{"resist"}, models.MatchOnly, nil, nil, testdb.TwilioChannel)
	emojiID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.PickANumber, []string{"👍"}, models.MatchFirst, nil, nil, nil)
	doctorsID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.SingleMessage, []string{"resist"}, models.MatchOnly, []*testdb.Group{testdb.DoctorsGroup}, nil, nil)
	doctorsAndNotTestersID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.SingleMessage, []string{"resist"}, models.MatchOnly, []*testdb.Group{testdb.DoctorsGroup}, []*testdb.Group{testdb.TestersGroup}, nil)
	doctorsCatchallID := testdb.InsertCatchallTrigger(rt, testdb.Org1, testdb.SingleMessage, []*testdb.Group{testdb.DoctorsGroup}, nil, nil)
	othersAllID := testdb.InsertCatchallTrigger(rt, testdb.Org1, testdb.SingleMessage, nil, nil, nil)

	// trigger for other org
	testdb.InsertCatchallTrigger(rt, testdb.Org2, testdb.Org2Favorites, nil, nil, nil)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	testdb.DoctorsGroup.Add(rt, testdb.Bob)
	testdb.TestersGroup.Add(rt, testdb.Bob)

	_, ann, _ := testdb.Ann.Load(rt, oa)
	_, george, _ := testdb.George.Load(rt, oa)
	_, bob, _ := testdb.Bob.Load(rt, oa)

	twilioChannel, _ := models.GetChannelByID(ctx, rt.DB.DB, testdb.TwilioChannel.ID)
	facebookChannel, _ := models.GetChannelByID(ctx, rt.DB.DB, testdb.FacebookChannel.ID)

	tcs := []struct {
		text              string
		channel           *models.Channel
		contact           *flows.Contact
		expectedTriggerID models.TriggerID
		expectedKeyword   string
	}{
		{" join ", nil, ann, joinID, "join"},
		{"JOIN", nil, ann, joinID, "join"},
		{"JOIN", twilioChannel, ann, joinTwilioOnlyID, "join"},
		{"JOIN", facebookChannel, ann, joinID, "join"},
		{"join this", nil, ann, joinID, "join"},
		{"resist", nil, george, resistID, "resist"},
		{"resist", twilioChannel, george, resistTwilioOnlyID, "resist"},
		{"resist", nil, bob, doctorsID, "resist"},
		{"resist", twilioChannel, ann, resistTwilioOnlyID, "resist"},
		{"resist", nil, ann, doctorsAndNotTestersID, "resist"},
		{"resist this", nil, ann, doctorsCatchallID, ""},
		{" 👍 ", nil, george, emojiID, "👍"},
		{"👍🏾", nil, george, emojiID, "👍"}, // is 👍 + 🏾
		{"😀👍", nil, george, othersAllID, ""},
		{"other", nil, ann, doctorsCatchallID, ""},
		{"other", nil, george, othersAllID, ""},
		{"", nil, george, othersAllID, ""},
		{"start", twilioChannel, ann, startTwilioOnlyID, "start"},
		{"start", facebookChannel, ann, doctorsCatchallID, ""},
		{"start", twilioChannel, george, startTwilioOnlyID, "start"},
		{"start", facebookChannel, george, othersAllID, ""},
	}

	for _, tc := range tcs {
		trigger, keyword := models.FindMatchingMsgTrigger(oa, tc.channel, tc.contact, tc.text)

		assertTrigger(t, tc.expectedTriggerID, trigger, "trigger mismatch for %s sending '%s'", tc.contact.Name(), tc.text)
		assert.Equal(t, tc.expectedKeyword, keyword, "keyword mismatch for %s sending '%s'", tc.contact.Name(), tc.text)
	}
}

func TestFindMatchingIncomingCallTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	doctorsAndNotTestersTriggerID := testdb.InsertIncomingCallTrigger(rt, testdb.Org1, testdb.Favorites, []*testdb.Group{testdb.DoctorsGroup}, []*testdb.Group{testdb.TestersGroup}, nil)
	doctorsTriggerID := testdb.InsertIncomingCallTrigger(rt, testdb.Org1, testdb.Favorites, []*testdb.Group{testdb.DoctorsGroup}, nil, nil)
	notTestersTriggerID := testdb.InsertIncomingCallTrigger(rt, testdb.Org1, testdb.Favorites, nil, []*testdb.Group{testdb.TestersGroup}, nil)
	everyoneTriggerID := testdb.InsertIncomingCallTrigger(rt, testdb.Org1, testdb.Favorites, nil, nil, nil)
	specificChannelTriggerID := testdb.InsertIncomingCallTrigger(rt, testdb.Org1, testdb.Favorites, nil, nil, testdb.TwilioChannel)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	testdb.DoctorsGroup.Add(rt, testdb.Bob)
	testdb.TestersGroup.Add(rt, testdb.Bob, testdb.Alexandra)

	_, ann, _ := testdb.Ann.Load(rt, oa)
	_, bob, _ := testdb.Bob.Load(rt, oa)
	_, george, _ := testdb.George.Load(rt, oa)
	_, alexa, _ := testdb.Alexandra.Load(rt, oa)

	twilioChannel, _ := models.GetChannelByID(ctx, rt.DB.DB, testdb.TwilioChannel.ID)
	facebookChannel, _ := models.GetChannelByID(ctx, rt.DB.DB, testdb.FacebookChannel.ID)

	tcs := []struct {
		contact           *flows.Contact
		channel           *models.Channel
		expectedTriggerID models.TriggerID
	}{
		{ann, twilioChannel, specificChannelTriggerID},        // specific channel
		{ann, facebookChannel, doctorsAndNotTestersTriggerID}, // not matching channel, get the next best scored channel
		{ann, nil, doctorsAndNotTestersTriggerID},             // they're in doctors and not in testers
		{bob, nil, doctorsTriggerID},                          // they're in doctors and testers
		{george, nil, notTestersTriggerID},                    // they're not in doctors and not in testers
		{alexa, nil, everyoneTriggerID},                       // they're not in doctors but are in testers
	}

	for _, tc := range tcs {
		trigger := models.FindMatchingIncomingCallTrigger(oa, tc.channel, tc.contact)

		assertTrigger(t, tc.expectedTriggerID, trigger, "trigger mismatch for %s", tc.contact.Name())
	}
}

func TestFindMatchingMissedCallTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertCatchallTrigger(rt, testdb.Org1, testdb.SingleMessage, nil, nil, nil)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	// no missed call trigger yet
	trigger := models.FindMatchingMissedCallTrigger(oa, nil)
	assert.Nil(t, trigger)

	triggerID := testdb.InsertMissedCallTrigger(rt, testdb.Org1, testdb.Favorites, nil)

	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	trigger = models.FindMatchingMissedCallTrigger(oa, nil)
	assertTrigger(t, triggerID, trigger)

	triggerIDwithChannel := testdb.InsertMissedCallTrigger(rt, testdb.Org1, testdb.Favorites, testdb.TwilioChannel)

	oa, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	channel, _ := models.GetChannelByID(ctx, rt.DB.DB, testdb.TwilioChannel.ID)

	trigger = models.FindMatchingMissedCallTrigger(oa, channel)
	assertTrigger(t, triggerIDwithChannel, trigger)

}

func TestFindMatchingNewConversationTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	twilioTriggerID := testdb.InsertNewConversationTrigger(rt, testdb.Org1, testdb.Favorites, testdb.TwilioChannel)
	noChTriggerID := testdb.InsertNewConversationTrigger(rt, testdb.Org1, testdb.Favorites, nil)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	tcs := []struct {
		channelID         models.ChannelID
		expectedTriggerID models.TriggerID
	}{
		{testdb.TwilioChannel.ID, twilioTriggerID},
		{testdb.VonageChannel.ID, noChTriggerID},
	}

	for i, tc := range tcs {
		channel := oa.ChannelByID(tc.channelID)
		trigger := models.FindMatchingNewConversationTrigger(oa, channel)

		assertTrigger(t, tc.expectedTriggerID, trigger, "trigger mismatch in test case #%d", i)
	}
}

func TestFindMatchingReferralTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	fooID := testdb.InsertReferralTrigger(rt, testdb.Org1, testdb.Favorites, "foo", testdb.FacebookChannel)
	barID := testdb.InsertReferralTrigger(rt, testdb.Org1, testdb.Favorites, "bar", nil)
	bazID := testdb.InsertReferralTrigger(rt, testdb.Org1, testdb.Favorites, "", testdb.FacebookChannel)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	tcs := []struct {
		referrerID        string
		channelID         models.ChannelID
		expectedTriggerID models.TriggerID
	}{
		{"", testdb.TwilioChannel.ID, models.NilTriggerID},
		{"foo", testdb.TwilioChannel.ID, models.NilTriggerID},
		{"foo", testdb.FacebookChannel.ID, fooID},
		{"FOO", testdb.FacebookChannel.ID, fooID},
		{"bar", testdb.TwilioChannel.ID, barID},
		{"bar", testdb.FacebookChannel.ID, barID},
		{"zap", testdb.TwilioChannel.ID, models.NilTriggerID},
		{"zap", testdb.FacebookChannel.ID, bazID},
	}

	for i, tc := range tcs {
		channel := oa.ChannelByID(tc.channelID)
		trigger := models.FindMatchingReferralTrigger(oa, channel, tc.referrerID)

		assertTrigger(t, tc.expectedTriggerID, trigger, "trigger mismatch in test case #%d", i)
	}
}

func TestFindMatchingOptInTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	twilioTriggerID := testdb.InsertOptInTrigger(rt, testdb.Org1, testdb.Favorites, testdb.TwilioChannel)
	noChTriggerID := testdb.InsertOptInTrigger(rt, testdb.Org1, testdb.Favorites, nil)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	tcs := []struct {
		channelID         models.ChannelID
		expectedTriggerID models.TriggerID
	}{
		{testdb.TwilioChannel.ID, twilioTriggerID},
		{testdb.VonageChannel.ID, noChTriggerID},
	}

	for i, tc := range tcs {
		channel := oa.ChannelByID(tc.channelID)
		trigger := models.FindMatchingOptInTrigger(oa, channel)

		assertTrigger(t, tc.expectedTriggerID, trigger, "trigger mismatch in test case #%d", i)
	}
}

func TestFindMatchingOptOutTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	twilioTriggerID := testdb.InsertOptOutTrigger(rt, testdb.Org1, testdb.Favorites, testdb.TwilioChannel)
	noChTriggerID := testdb.InsertOptOutTrigger(rt, testdb.Org1, testdb.Favorites, nil)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTriggers)
	require.NoError(t, err)

	tcs := []struct {
		channelID         models.ChannelID
		expectedTriggerID models.TriggerID
	}{
		{testdb.TwilioChannel.ID, twilioTriggerID},
		{testdb.VonageChannel.ID, noChTriggerID},
	}

	for i, tc := range tcs {
		channel := oa.ChannelByID(tc.channelID)
		trigger := models.FindMatchingOptOutTrigger(oa, channel)

		assertTrigger(t, tc.expectedTriggerID, trigger, "trigger mismatch in test case #%d", i)
	}
}

func TestArchiveContactTriggers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	everybodyID := testdb.InsertKeywordTrigger(rt, testdb.Org1, testdb.Favorites, []string{"join"}, models.MatchFirst, nil, nil, nil)
	annOnly1ID := testdb.InsertScheduledTrigger(rt, testdb.Org1, testdb.Favorites, testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodMonthly, time.Now()), nil, nil, []*testdb.Contact{testdb.Ann})
	annOnly2ID := testdb.InsertScheduledTrigger(rt, testdb.Org1, testdb.Favorites, testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodMonthly, time.Now()), nil, nil, []*testdb.Contact{testdb.Ann})
	annAndGeorgeID := testdb.InsertScheduledTrigger(rt, testdb.Org1, testdb.Favorites, testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodMonthly, time.Now()), nil, nil, []*testdb.Contact{testdb.Ann, testdb.George})
	annAndGroupID := testdb.InsertScheduledTrigger(rt, testdb.Org1, testdb.Favorites, testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodMonthly, time.Now()), []*testdb.Group{testdb.DoctorsGroup}, nil, []*testdb.Contact{testdb.Ann})
	georgeOnlyID := testdb.InsertScheduledTrigger(rt, testdb.Org1, testdb.Favorites, testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodMonthly, time.Now()), nil, nil, []*testdb.Contact{testdb.George})

	err := models.ArchiveContactTriggers(ctx, rt.DB, []models.ContactID{testdb.Ann.ID, testdb.Bob.ID})
	require.NoError(t, err)

	assertTriggerArchived := func(id models.TriggerID, archived bool) {
		var isArchived bool
		rt.DB.Get(&isArchived, `SELECT is_archived FROM triggers_trigger WHERE id = $1`, id)
		assert.Equal(t, archived, isArchived, `is_archived mismatch for trigger %d`, id)
	}

	assertTriggerArchived(everybodyID, false)
	assertTriggerArchived(annOnly1ID, true)
	assertTriggerArchived(annOnly2ID, true)
	assertTriggerArchived(annAndGeorgeID, false)
	assertTriggerArchived(annAndGroupID, false)
	assertTriggerArchived(georgeOnlyID, false)
}

func assertTrigger(t *testing.T, expected models.TriggerID, actual *models.Trigger, msgAndArgs ...any) {
	if actual == nil {
		assert.Equal(t, expected, models.NilTriggerID, msgAndArgs...)
	} else {
		assert.Equal(t, expected, actual.ID(), msgAndArgs...)
	}
}
