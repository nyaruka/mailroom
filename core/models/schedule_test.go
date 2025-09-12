package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSchedule(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(rt)

	dates.SetNowFunc(dates.NewFixedNow(time.Date(2024, 6, 20, 14, 30, 0, 0, time.UTC)))
	defer dates.SetNowFunc(time.Now)

	_, err := models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 46, 30, 0, time.UTC), "Z", "")
	assert.EqualError(t, err, "invalid repeat period: Z")

	// create one off schedule
	sched, err := models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 45, 55, 0, time.UTC), models.RepeatPeriodNever, "")
	assert.NoError(t, err)
	assert.Equal(t, testdb.Org1.ID, sched.OrgID)
	assert.Equal(t, models.RepeatPeriodNever, sched.RepeatPeriod)
	assert.Equal(t, time.Date(2024, 6, 20, 7, 45, 55, 0, oa.Env().Timezone()), *sched.NextFire)

	// create daily schedule with start in the future
	sched, err = models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 45, 55, 0, time.UTC), models.RepeatPeriodDaily, "")
	assert.NoError(t, err)
	assert.Equal(t, testdb.Org1.ID, sched.OrgID)
	assert.Equal(t, models.RepeatPeriodDaily, sched.RepeatPeriod)
	assert.Equal(t, 7, *sched.RepeatHourOfDay)
	assert.Equal(t, 45, *sched.RepeatMinuteOfHour)
	assert.Equal(t, time.Date(2024, 6, 20, 7, 45, 55, 0, oa.Env().Timezone()), *sched.NextFire)

	// create daily schedule with start in the past
	sched, err = models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 15, 55, 0, time.UTC), models.RepeatPeriodDaily, "")
	assert.NoError(t, err)
	assert.Equal(t, testdb.Org1.ID, sched.OrgID)
	assert.Equal(t, models.RepeatPeriodDaily, sched.RepeatPeriod)
	assert.Equal(t, 7, *sched.RepeatHourOfDay)
	assert.Equal(t, 15, *sched.RepeatMinuteOfHour)
	assert.Equal(t, time.Date(2024, 6, 21, 7, 15, 0, 0, oa.Env().Timezone()), *sched.NextFire) // calculated

	_, err = models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 45, 55, 0, time.UTC), models.RepeatPeriodWeekly, "")
	assert.EqualError(t, err, "weekly repeating schedules must specify days of the week")

	// create weekly schedule with start in the future
	sched, err = models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 45, 55, 0, time.UTC), models.RepeatPeriodWeekly, "MF")
	assert.NoError(t, err)
	assert.Equal(t, testdb.Org1.ID, sched.OrgID)
	assert.Equal(t, models.RepeatPeriodWeekly, sched.RepeatPeriod)
	assert.Equal(t, 7, *sched.RepeatHourOfDay)
	assert.Equal(t, 45, *sched.RepeatMinuteOfHour)
	assert.Equal(t, "MF", string(sched.RepeatDaysOfWeek))
	assert.Equal(t, time.Date(2024, 6, 20, 7, 45, 55, 0, oa.Env().Timezone()), *sched.NextFire)

	// create weekly schedule with start in the past
	sched, err = models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 15, 55, 0, time.UTC), models.RepeatPeriodWeekly, "MF")
	assert.NoError(t, err)
	assert.Equal(t, testdb.Org1.ID, sched.OrgID)
	assert.Equal(t, models.RepeatPeriodWeekly, sched.RepeatPeriod)
	assert.Equal(t, 7, *sched.RepeatHourOfDay)
	assert.Equal(t, 15, *sched.RepeatMinuteOfHour)
	assert.Equal(t, "MF", string(sched.RepeatDaysOfWeek))
	assert.Equal(t, time.Date(2024, 6, 21, 7, 15, 0, 0, oa.Env().Timezone()), *sched.NextFire)

	// create monthly schedule with start in the past
	sched, err = models.NewSchedule(oa, time.Date(2024, 6, 20, 14, 15, 55, 0, time.UTC), models.RepeatPeriodMonthly, "")
	assert.NoError(t, err)
	assert.Equal(t, testdb.Org1.ID, sched.OrgID)
	assert.Equal(t, models.RepeatPeriodMonthly, sched.RepeatPeriod)
	assert.Equal(t, 7, *sched.RepeatHourOfDay)
	assert.Equal(t, 15, *sched.RepeatMinuteOfHour)
	assert.Equal(t, 20, *sched.RepeatDayOfMonth)
	assert.Equal(t, time.Date(2024, 7, 20, 7, 15, 0, 0, oa.Env().Timezone()), *sched.NextFire)
}

func TestGetExpired(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	optIn := testdb.InsertOptIn(rt, testdb.Org1, "Polls")

	// add a schedule and tie a broadcast to it
	s1 := testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodNever, time.Now().Add(-24*time.Hour))

	testdb.InsertBroadcast(rt, testdb.Org1, "eng", map[i18n.Language]string{"eng": "Test message", "fra": "Un Message"}, optIn, s1,
		[]*testdb.Contact{testdb.Ann, testdb.George}, []*testdb.Group{testdb.DoctorsGroup},
	)

	// add another and tie a trigger to it
	s2 := testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodNever, time.Now().Add(-48*time.Hour))

	testdb.InsertScheduledTrigger(rt, testdb.Org1, testdb.Favorites, s2, []*testdb.Group{testdb.DoctorsGroup}, nil, []*testdb.Contact{testdb.Ann, testdb.George})

	s3 := testdb.InsertSchedule(rt, testdb.Org1, models.RepeatPeriodNever, time.Now().Add(-72*time.Hour))

	// get expired schedules
	schedules, err := models.GetUnfiredSchedules(ctx, rt.DB.DB)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(schedules))

	assert.Equal(t, s3, schedules[0].ID)
	assert.Nil(t, schedules[0].Broadcast)
	assert.Equal(t, models.RepeatPeriodNever, schedules[0].RepeatPeriod)
	assert.NotNil(t, schedules[0].NextFire)
	assert.Nil(t, schedules[0].LastFire)

	assert.Equal(t, s2, schedules[1].ID)
	assert.Nil(t, schedules[1].Broadcast)

	trigger := schedules[1].Trigger
	assert.NotNil(t, trigger)
	assert.Equal(t, testdb.Favorites.ID, trigger.FlowID())
	assert.Equal(t, testdb.Org1.ID, trigger.OrgID())
	assert.Equal(t, []models.ContactID{testdb.Ann.ID, testdb.George.ID}, trigger.ContactIDs())
	assert.Equal(t, []models.GroupID{testdb.DoctorsGroup.ID}, trigger.IncludeGroupIDs())

	assert.Equal(t, s1, schedules[2].ID)
	bcast := schedules[2].Broadcast
	assert.NotNil(t, bcast)
	assert.Equal(t, i18n.Language("eng"), bcast.BaseLanguage)
	assert.Equal(t, "Test message", bcast.Translations["eng"].Text)
	assert.Equal(t, "Un Message", bcast.Translations["fra"].Text)
	assert.True(t, bcast.Expressions)
	assert.Equal(t, optIn.ID, bcast.OptInID)
	assert.Equal(t, testdb.Org1.ID, bcast.OrgID)
	assert.Equal(t, []models.ContactID{testdb.Ann.ID, testdb.George.ID}, bcast.ContactIDs)
	assert.Equal(t, []models.GroupID{testdb.DoctorsGroup.ID}, bcast.GroupIDs)
}

func TestGetNextFire(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	tcs := []struct {
		Label         string
		Now           time.Time
		Timezone      string
		Schedule      json.RawMessage
		ExpectedNexts []time.Time
		ExpectedError string
	}{
		{
			Label:         "no hour of day set",
			Now:           time.Date(2019, 8, 20, 10, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "D"}`),
			ExpectedError: "no repeat_hour_of_day set",
		},
		{
			Label:         "no minute of hour set",
			Now:           time.Date(2019, 8, 20, 10, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "D", "repeat_hour_of_day": 12}`),
			ExpectedError: "no repeat_minute_of_hour set",
		},
		{
			Label:         "unknown repeat period",
			Now:           time.Date(2019, 8, 20, 10, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "Z", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedError: "unknown repeat period: Z",
		},
		{
			Label:         "never repeat",
			Now:           time.Date(2019, 8, 20, 10, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "O"}`),
			ExpectedNexts: []time.Time{},
		},
		{
			Label:         "daily repeat on same day",
			Now:           time.Date(2019, 8, 20, 10, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "D", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 8, 20, 12, 35, 0, 0, la)},
		},
		{
			Label:         "daily repeat on same hour minute",
			Now:           time.Date(2019, 8, 20, 12, 35, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "D", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 8, 21, 12, 35, 0, 0, la)},
		},
		{
			Label:         "daily repeat for next day",
			Now:           time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "D", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 8, 21, 12, 35, 0, 0, la)},
		},
		{
			Label:    "daily repeat for next day across DST start",
			Now:      time.Date(2019, 3, 9, 12, 30, 0, 0, la),
			Timezone: "America/Los_Angeles",
			Schedule: []byte(`{"repeat_period": "D", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 30}`),
			ExpectedNexts: []time.Time{
				time.Date(2019, 3, 10, 12, 30, 0, 0, la),
				time.Date(2019, 3, 11, 12, 30, 0, 0, la),
			},
		},
		{
			Label:    "daily repeat for next day across DST end",
			Now:      time.Date(2019, 11, 2, 12, 30, 0, 0, la),
			Timezone: "America/Los_Angeles",
			Schedule: []byte(`{"repeat_period": "D", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 30}`),
			ExpectedNexts: []time.Time{
				time.Date(2019, 11, 3, 12, 30, 0, 0, la),
				time.Date(2019, 11, 4, 12, 30, 0, 0, la),
			},
		},
		{
			Label:         "weekly repeat missing days of week",
			Now:           time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "W", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedError: "repeats weekly but has no repeat_days_of_week",
		},
		{
			Label:         "weekly with invalid day of week",
			Now:           time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "W", "repeat_days_of_week": "Z", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedError: "unknown day of week: Z",
		},
		{
			Label:    "weekly repeat to day later in week",
			Now:      time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone: "America/Los_Angeles",
			Schedule: []byte(`{"repeat_period": "W", "repeat_days_of_week": "RU", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{
				time.Date(2019, 8, 22, 12, 35, 0, 0, la),
				time.Date(2019, 8, 25, 12, 35, 0, 0, la),
				time.Date(2019, 8, 29, 12, 35, 0, 0, la),
			},
		},
		{
			Label:    "weekly repeat to day later in week using fire date",
			Now:      time.Date(2019, 8, 26, 12, 35, 0, 0, la),
			Timezone: "America/Los_Angeles",
			Schedule: []byte(`{"repeat_period": "W", "repeat_days_of_week": "MTWRFSU", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{
				time.Date(2019, 8, 27, 12, 35, 0, 0, la),
				time.Date(2019, 8, 28, 12, 35, 0, 0, la),
				time.Date(2019, 8, 29, 12, 35, 0, 0, la),
				time.Date(2019, 8, 30, 12, 35, 0, 0, la),
				time.Date(2019, 8, 31, 12, 35, 0, 0, la),
				time.Date(2019, 9, 1, 12, 35, 0, 0, la),
			},
		},
		{
			Label:         "weekly repeat for next day across DST",
			Now:           time.Date(2019, 3, 9, 12, 30, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "W", "repeat_days_of_week": "MTWRFSU", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 30}`),
			ExpectedNexts: []time.Time{time.Date(2019, 3, 10, 12, 30, 0, 0, la)},
		},
		{
			Label:         "weekly repeat to day in next week",
			Now:           time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "W", "repeat_days_of_week": "M", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 8, 26, 12, 35, 0, 0, la)},
		},
		{
			Label:         "monthly repeat with no day of month set",
			Now:           time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "M", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedError: "repeats monthly but has no repeat_day_of_month",
		},
		{
			Label:    "monthly repeat to day in same month",
			Now:      time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone: "America/Los_Angeles",
			Schedule: []byte(`{"repeat_period": "M", "repeat_day_of_month": 31, "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{
				time.Date(2019, 8, 31, 12, 35, 0, 0, la),
				time.Date(2019, 9, 30, 12, 35, 0, 0, la),
				time.Date(2019, 10, 31, 12, 35, 0, 0, la),
				time.Date(2019, 11, 30, 12, 35, 0, 0, la),
				time.Date(2019, 12, 31, 12, 35, 0, 0, la),
				time.Date(2020, 1, 31, 12, 35, 0, 0, la),
			},
		},
		{
			Label:    "monthly repeat to day in same month from fire date",
			Now:      time.Date(2019, 8, 20, 12, 35, 0, 0, la),
			Timezone: "America/Los_Angeles",
			Schedule: []byte(`{"repeat_period": "M", "repeat_day_of_month": 20, "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{
				time.Date(2019, 9, 20, 12, 35, 0, 0, la),
				time.Date(2019, 10, 20, 12, 35, 0, 0, la),
				time.Date(2019, 11, 20, 12, 35, 0, 0, la),
				time.Date(2019, 12, 20, 12, 35, 0, 0, la),
				time.Date(2020, 1, 20, 12, 35, 0, 0, la),
			},
		},
		{
			Label:         "monthly repeat to day in next month",
			Now:           time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "M", "repeat_day_of_month": 5, "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 9, 5, 12, 35, 0, 0, la)},
		},
		{
			Label:         "monthly repeat to day that exceeds month",
			Now:           time.Date(2019, 9, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "M", "repeat_day_of_month": 31, "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 9, 30, 12, 35, 0, 0, la)},
		},
		{
			Label:         "monthly repeat to day in next month that exceeds month",
			Now:           time.Date(2019, 8, 31, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "M", "repeat_day_of_month": 31, "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 9, 30, 12, 35, 0, 0, la)},
		},
		{
			Label:         "monthy repeat for next month across DST",
			Now:           time.Date(2019, 2, 10, 12, 30, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "M", "repeat_day_of_month": 10, "repeat_hour_of_day": 12, "repeat_minute_of_hour": 30}`),
			ExpectedNexts: []time.Time{time.Date(2019, 3, 10, 12, 30, 0, 0, la)},
		},
		{
			Label:         "yearly repeat for future time",
			Now:           time.Date(2019, 8, 20, 10, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "Y", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2019, 8, 20, 12, 35, 0, 0, la)},
		},
		{
			Label:         "yearly repeat on same hour minute",
			Now:           time.Date(2019, 8, 20, 12, 35, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "Y", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2020, 8, 20, 12, 35, 0, 0, la)},
		},
		{
			Label:         "yearly repeat for past time",
			Now:           time.Date(2019, 8, 20, 13, 57, 0, 0, la),
			Timezone:      "America/Los_Angeles",
			Schedule:      []byte(`{"repeat_period": "Y", "repeat_hour_of_day": 12, "repeat_minute_of_hour": 35}`),
			ExpectedNexts: []time.Time{time.Date(2020, 8, 20, 12, 35, 0, 0, la)},
		},
	}

	for _, tc := range tcs {
		sched := &models.Schedule{}
		jsonx.MustUnmarshal(tc.Schedule, sched)
		sched.Timezone = tc.Timezone

		if tc.ExpectedError != "" {
			next, err := sched.GetNextFire(tc.Now)
			assert.EqualError(t, err, tc.ExpectedError, "%s: error mismatch", tc.Label)
			assert.Nil(t, next)
		} else if len(tc.ExpectedNexts) == 0 {
			next, err := sched.GetNextFire(tc.Now)
			assert.NoError(t, err, "%s: unexpected error", tc.Label)
			assert.Nil(t, next, "%s: unexpected next fire", tc.Label)
		} else {
			actualNexts := make([]time.Time, len(tc.ExpectedNexts))
			now := tc.Now
			for i := range tc.ExpectedNexts {
				next, err := sched.GetNextFire(now)
				assert.NoError(t, err, "%s: unexpected error", tc.Label)
				if assert.NotNil(t, next, "%s: unexpected nil next", tc.Label) {
					actualNexts[i] = *next
					now = *next
				}
			}

			assert.Equal(t, tc.ExpectedNexts, actualNexts, "%s: next fires mismatch", tc.Label)
		}
	}
}
