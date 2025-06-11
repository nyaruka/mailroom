package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/null/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCampaigns(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, 1, models.RefreshChannels)
	require.NoError(t, err)

	event1 := oa.CampaignEventByID(testdb.RemindersEvent1.ID)
	assert.Equal(t, testdb.RemindersEvent1.ID, event1.ID)
	assert.Equal(t, testdb.RemindersEvent1.UUID, event1.UUID)
	assert.Nil(t, event1.Translations)

	event2 := oa.CampaignEventByID(testdb.RemindersEvent2.ID)
	assert.Equal(t, testdb.RemindersEvent2.UUID, event2.UUID)
	assert.Equal(t, flows.BroadcastTranslations{
		"eng": &flows.MsgContent{Text: "Hi @contact.name, it is time to consult with your patients."},
		"fra": &flows.MsgContent{Text: "Bonjour @contact.name, il est temps de consulter vos patients."},
	}, event2.Translations)
	assert.Equal(t, null.String("eng"), event2.BaseLanguage)

	event3 := oa.CampaignEventByID(testdb.RemindersEvent3.ID)
	assert.Equal(t, testdb.RemindersEvent3.UUID, event3.UUID)
}

func TestScheduleForTime(t *testing.T) {
	eastern, _ := time.LoadLocation("US/Eastern")
	nilDate := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)

	tcs := []struct {
		eventOffset       int
		eventUnit         models.CampaignEventUnit
		eventDeliveryHour int
		timezone          *time.Location
		now               time.Time
		start             time.Time
		expectedScheduled time.Time
		expectedDelta     time.Duration
	}{
		{ // 0: crosses a DST boundary, so two days is really 49 hours (fall back)
			2, models.CampaignEventUnitDays, models.NilDeliveryHour,
			eastern, time.Now(), time.Date(2029, 11, 3, 0, 30, 0, 0, eastern),
			time.Date(2029, 11, 5, 0, 30, 0, 0, eastern), time.Hour * 49,
		},
		{ // 1: also crosses a boundary but in the other direction
			2, models.CampaignEventUnitDays, models.NilDeliveryHour,
			eastern, time.Now(), time.Date(2029, 3, 10, 2, 30, 0, 0, eastern),
			time.Date(2029, 3, 12, 2, 30, 0, 0, eastern), time.Hour * 47,
		},
		{ // 2: this event is in the past, no schedule
			2, models.CampaignEventUnitDays, models.NilDeliveryHour,
			eastern, time.Date(2018, 10, 31, 0, 0, 0, 0, eastern), time.Date(2018, 10, 15, 0, 0, 0, 0, eastern),
			nilDate, 0,
		},
		{ // 3
			2, models.CampaignEventUnitMinutes, models.NilDeliveryHour,
			eastern, time.Now(), time.Date(2029, 1, 1, 2, 58, 0, 0, eastern),
			time.Date(2029, 1, 1, 3, 0, 0, 0, eastern), time.Minute * 2,
		},
		{ // 4
			2, models.CampaignEventUnitMinutes, models.NilDeliveryHour,
			eastern, time.Now(), time.Date(2029, 1, 1, 2, 57, 32, 0, eastern),
			time.Date(2029, 1, 1, 3, 0, 0, 0, eastern), time.Minute*2 + time.Second*28,
		},
		{ // 5
			-2, models.CampaignEventUnitHours, models.NilDeliveryHour,
			eastern, time.Now(), time.Date(2029, 1, 2, 1, 58, 0, 0, eastern),
			time.Date(2029, 1, 1, 23, 58, 0, 0, eastern), time.Hour * -2,
		},
		{ // 6
			2, models.CampaignEventUnitWeeks, models.NilDeliveryHour,
			eastern, time.Now(), time.Date(2029, 1, 20, 1, 58, 0, 0, eastern),
			time.Date(2029, 2, 3, 1, 58, 0, 0, eastern), time.Hour * 24 * 14,
		},
		{ // 7
			2, models.CampaignEventUnitWeeks, 14,
			eastern, time.Now(), time.Date(2029, 1, 20, 1, 58, 0, 0, eastern),
			time.Date(2029, 2, 3, 14, 0, 0, 0, eastern), time.Hour*24*14 + 13*time.Hour - 58*time.Minute,
		},
	}

	for i, tc := range tcs {
		evt := &models.CampaignEvent{
			Offset:       tc.eventOffset,
			Unit:         tc.eventUnit,
			DeliveryHour: tc.eventDeliveryHour,
		}

		scheduled := evt.ScheduleForTime(tc.timezone, tc.now, tc.start)

		if tc.expectedScheduled.IsZero() {
			assert.Nil(t, scheduled, "%d: received unexpected value", i)
		} else {
			assert.Equal(t, tc.expectedScheduled.In(time.UTC), scheduled.In(time.UTC), "%d: mismatch in expected scheduled and actual", i)
			assert.Equal(t, scheduled.Sub(tc.start), tc.expectedDelta, "%d: mismatch in expected delta", i)
		}
	}
}
