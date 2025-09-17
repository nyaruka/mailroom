package testdb

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/stretchr/testify/require"
)

func InsertSchedule(t *testing.T, rt *runtime.Runtime, org *Org, repeat models.RepeatPeriod, next time.Time) models.ScheduleID {
	var id models.ScheduleID
	err := rt.DB.Get(&id,
		`INSERT INTO schedules_schedule(org_id, repeat_period, repeat_hour_of_day, repeat_minute_of_hour, next_fire, is_paused)
		VALUES($1, $2, 12, 0, $3, FALSE) RETURNING id`, org.ID, repeat, next,
	)
	require.NoError(t, err)

	return id
}
