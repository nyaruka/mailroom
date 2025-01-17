package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/null/v3"
)

// ScheduleID is our internal type for schedule IDs
type ScheduleID int

// NilScheduleID is our constant for a nil schedule id
const NilScheduleID = ScheduleID(0)

// RepeatPeriod is the different ways a schedule can repeat
type RepeatPeriod string

const (
	RepeatPeriodNever   = RepeatPeriod("O")
	RepeatPeriodDaily   = RepeatPeriod("D")
	RepeatPeriodWeekly  = RepeatPeriod("W")
	RepeatPeriodMonthly = RepeatPeriod("M")
	RepeatPeriodYearly  = RepeatPeriod("Y")
)

// day of the week constants for weekly repeating schedules
const (
	Monday    = 'M'
	Tuesday   = 'T'
	Wednesday = 'W'
	Thursday  = 'R'
	Friday    = 'F'
	Saturday  = 'S'
	Sunday    = 'U'
)

var dayStrToDayInt = map[rune]time.Weekday{
	Sunday:    0,
	Monday:    1,
	Tuesday:   2,
	Wednesday: 3,
	Thursday:  4,
	Friday:    5,
	Saturday:  6,
}

// Schedule represents a scheduled event
type Schedule struct {
	ID                 ScheduleID   `db:"id"                    json:"id"`
	OrgID              OrgID        `db:"org_id"                json:"org_id"`
	RepeatPeriod       RepeatPeriod `db:"repeat_period"         json:"repeat_period"`
	RepeatHourOfDay    *int         `db:"repeat_hour_of_day"    json:"repeat_hour_of_day"`
	RepeatMinuteOfHour *int         `db:"repeat_minute_of_hour" json:"repeat_minute_of_hour"`
	RepeatDaysOfWeek   null.String  `db:"repeat_days_of_week"   json:"repeat_days_of_week"`
	RepeatDayOfMonth   *int         `db:"repeat_day_of_month"   json:"repeat_day_of_month"`
	NextFire           *time.Time   `db:"next_fire"             json:"next_fire"`
	LastFire           *time.Time   `db:"last_fire"             json:"last_fire"`
	IsPaused           bool         `db:"is_paused"`

	// target that schedule has been loaded with
	Broadcast *Broadcast `json:"broadcast,omitempty"`
	Trigger   *Trigger   `json:"trigger,omitempty"`
	Timezone  string     `json:"timezone"`
}

// NewSchedule creates a new schedule object
func NewSchedule(oa *OrgAssets, start time.Time, repeatPeriod RepeatPeriod, repeatDaysOfWeek string) (*Schedule, error) {
	// get start time in org timezone so that we always fire at the appropriate time regardless of timezone / dst changes
	tz := oa.Env().Timezone()
	start = start.In(tz)

	s := &Schedule{
		OrgID:        oa.OrgID(),
		RepeatPeriod: repeatPeriod,
		Timezone:     tz.String(),
	}

	if s.RepeatPeriod == RepeatPeriodNever {
		s.NextFire = &start
	} else {
		hour, minute := start.Hour(), start.Minute()
		s.RepeatHourOfDay = &hour
		s.RepeatMinuteOfHour = &minute

		if repeatPeriod == RepeatPeriodDaily || repeatPeriod == RepeatPeriodYearly {

		} else if repeatPeriod == RepeatPeriodWeekly {
			if repeatDaysOfWeek == "" {
				return nil, errors.New("weekly repeating schedules must specify days of the week")
			}
			for _, day := range repeatDaysOfWeek {
				_, found := dayStrToDayInt[day]
				if !found {
					return nil, fmt.Errorf("unknown day of week: %s", string(day))
				}
			}

			s.RepeatDaysOfWeek = null.String(repeatDaysOfWeek)
		} else if repeatPeriod == RepeatPeriodMonthly {
			day := start.Day()
			s.RepeatDayOfMonth = &day
		} else {
			return nil, fmt.Errorf("invalid repeat period: %s", repeatPeriod)
		}

		// if the given start time is in the past, calculate next fire in the future
		if start.Before(dates.Now()) {
			next, err := s.GetNextFire(start)
			if err != nil {
				return nil, err
			}
			s.NextFire = next
		} else {
			s.NextFire = &start
		}
	}

	return s, nil
}

const sqlInsertSchedule = `
INSERT INTO schedules_schedule( org_id,  repeat_period,  repeat_hour_of_day,  repeat_minute_of_hour,  repeat_days_of_week,  repeat_day_of_month,  next_fire,  is_paused)
	                    VALUES(:org_id, :repeat_period, :repeat_hour_of_day, :repeat_minute_of_hour, :repeat_days_of_week, :repeat_day_of_month, :next_fire,      FALSE)
  RETURNING id`

func (s *Schedule) Insert(ctx context.Context, db DBorTx) error {
	return BulkQuery(ctx, "insert schedule", db, sqlInsertSchedule, []any{s})
}

func (s *Schedule) GetTimezone() (*time.Location, error) {
	return time.LoadLocation(s.Timezone)
}

func (s *Schedule) GetRepeatDaysOfWeek() ([]time.Weekday, error) {
	days := make([]time.Weekday, len(s.RepeatDaysOfWeek))

	for i, dayChar := range s.RepeatDaysOfWeek {
		day, found := dayStrToDayInt[dayChar]
		if !found {
			return nil, fmt.Errorf("unknown day of week: %s", string(dayChar))
		}
		days[i] = day
	}
	return days, nil
}

// DeleteWithTarget deactivates this schedule along with its associated broadcast or flow start
func (s *Schedule) DeleteWithTarget(ctx context.Context, tx *sql.Tx) error {
	if s.Broadcast != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE msgs_broadcast SET is_active = FALSE, schedule_id = NULL WHERE id = $1`, s.Broadcast.ID); err != nil {
			return fmt.Errorf("error deactivating scheduled broadcast: %w", err)
		}
	} else if s.Trigger != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE triggers_trigger SET is_active = FALSE, schedule_id = NULL WHERE id = $1`, s.Trigger.ID()); err != nil {
			return fmt.Errorf("error deactivating scheduled trigger: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM schedules_schedule WHERE id = $1`, s.ID); err != nil {
		return fmt.Errorf("error deleting schedule: %w", err)
	}

	return nil
}

// UpdateFires updates the next and last fire for a shedule on the db
func (s *Schedule) UpdateFires(ctx context.Context, tx DBorTx, last time.Time, next *time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE schedules_schedule SET last_fire = $2, next_fire = $3 WHERE id = $1`,
		s.ID, last, next,
	)
	if err != nil {
		return fmt.Errorf("error updating schedule fire dates for: %d: %w", s.ID, err)
	}
	return nil
}

// GetNextFire returns the next fire for this schedule (if any)
func (s *Schedule) GetNextFire(now time.Time) (*time.Time, error) {
	// Never repeats? no next fire
	if s.RepeatPeriod == RepeatPeriodNever {
		return nil, nil
	}

	// should have hour and minute on everything else
	if s.RepeatHourOfDay == nil {
		return nil, errors.New("no repeat_hour_of_day set")
	}
	if s.RepeatMinuteOfHour == nil {
		return nil, errors.New("no repeat_minute_of_hour set")
	}
	tz, err := s.GetTimezone()
	if err != nil {
		return nil, fmt.Errorf("error loading timezone: %w", err)
	}

	// increment now by a minute, we don't want to double schedule in case of small clock drifts between boxes or db
	now = now.Add(time.Minute)

	// change our time to be in our location
	start := now.In(tz)
	minute := *s.RepeatMinuteOfHour
	hour := *s.RepeatHourOfDay

	// set our next fire to today at the specified hour and minute
	next := time.Date(start.Year(), start.Month(), start.Day(), hour, minute, 0, 0, tz)

	switch s.RepeatPeriod {

	case RepeatPeriodDaily:
		for !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		return &next, nil

	case RepeatPeriodWeekly:
		if s.RepeatDaysOfWeek == "" {
			return nil, errors.New("repeats weekly but has no repeat_days_of_week")
		}

		// get the days we repeat on
		sendDays, err := s.GetRepeatDaysOfWeek()
		if err != nil {
			return nil, err
		}

		// until we are in the future, increment a day until we reach a day of week we send on
		for !next.After(now) || !slices.Contains(sendDays, next.Weekday()) {
			next = next.AddDate(0, 0, 1)
		}

		return &next, nil

	case RepeatPeriodMonthly:
		if s.RepeatDayOfMonth == nil {
			return nil, errors.New("repeats monthly but has no repeat_day_of_month")
		}

		// figure out our next fire day, in the case that they asked for a day greater than the number of days
		// in a month, fire on the last day of the month instead
		day := *s.RepeatDayOfMonth
		maxDay := daysInMonth(next)
		if day > maxDay {
			day = maxDay
		}
		next = time.Date(next.Year(), next.Month(), day, hour, minute, 0, 0, tz)

		// this is in the past, move forward a month
		for !next.After(now) {
			next = time.Date(next.Year(), next.Month()+1, 1, hour, minute, 0, 0, tz)
			day = *s.RepeatDayOfMonth
			maxDay = daysInMonth(next)
			if day > maxDay {
				day = maxDay
			}
			next = time.Date(next.Year(), next.Month(), day, hour, minute, 0, 0, tz)
		}

		return &next, nil
	case RepeatPeriodYearly:
		for !next.After(now) {
			next = next.AddDate(1, 0, 0)
		}
		return &next, nil

	default:
		return nil, fmt.Errorf("unknown repeat period: %s", s.RepeatPeriod)
	}
}

// returns number of days in the month for the passed in date using crazy golang date magic
func daysInMonth(t time.Time) int {
	// day 0 of a month is previous day of previous month, months can be > 12 and roll years
	lastDay := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location())
	return lastDay.Day()
}

const sqlSelectUnfiredSchedules = `
SELECT ROW_TO_JSON(s) FROM (
    SELECT
        s.id,
        s.org_id,
        s.repeat_hour_of_day,
        s.repeat_minute_of_hour,
        s.repeat_day_of_month,
        s.repeat_days_of_week,
        s.repeat_period,
        s.next_fire,
        s.last_fire,
        o.timezone AS timezone,
        (SELECT ROW_TO_JSON(sb) FROM (
            SELECT
                b.id AS broadcast_id,
                s.org_id,
                b.translations,
                b.base_language,
                TRUE AS expressions,
                b.optin_id,
                b.template_id,
                b.template_variables,
                (SELECT ARRAY_AGG(bc.contact_id) FROM (SELECT contact_id FROM msgs_broadcast_contacts WHERE broadcast_id = b.id) bc) AS contact_ids,
                (SELECT ARRAY_AGG(bg.contactgroup_id) FROM (SELECT contactgroup_id FROM msgs_broadcast_groups WHERE broadcast_id = b.id) bg) AS group_ids
            FROM
                msgs_broadcast b
            WHERE
                b.schedule_id = s.id
        ) sb) AS broadcast,
        (SELECT ROW_TO_JSON(r) FROM (
            SELECT 
                t.id,
                t.org_id,
                t.flow_id, 
                'S' AS trigger_type,
                (SELECT ARRAY_AGG(tc.contact_id) FROM (SELECT contact_id FROM triggers_trigger_contacts WHERE trigger_id = t.id) tc) AS contact_ids,
                (SELECT ARRAY_AGG(tg.contactgroup_id) FROM (SELECT contactgroup_id FROM triggers_trigger_groups WHERE trigger_id = t.id) tg) AS include_group_ids,
                (SELECT ARRAY_AGG(te.contactgroup_id) FROM (SELECT contactgroup_id FROM triggers_trigger_exclude_groups WHERE trigger_id = t.id) te) AS exclude_group_ids
            FROM triggers_trigger t 
            WHERE t.schedule_id = s.id AND t.is_active = TRUE AND t.is_archived = FALSE
        ) r) AS trigger
        FROM schedules_schedule s 
        JOIN orgs_org o ON s.org_id = o.id
       WHERE s.next_fire < NOW() AND NOT is_paused 
    ORDER BY s.next_fire ASC
) s;`

// GetUnfiredSchedules returns all unfired schedules
func GetUnfiredSchedules(ctx context.Context, db *sql.DB) ([]*Schedule, error) {
	rows, err := db.QueryContext(ctx, sqlSelectUnfiredSchedules)
	if err != nil {
		return nil, fmt.Errorf("error selecting unfired schedules: %w", err)
	}
	defer rows.Close()

	unfired := make([]*Schedule, 0, 10)
	for rows.Next() {
		s := &Schedule{}
		err := dbutil.ScanJSON(rows, &s)
		if err != nil {
			return nil, fmt.Errorf("error reading schedule: %w", err)
		}
		unfired = append(unfired, s)
	}

	return unfired, nil
}

func (i *ScheduleID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i ScheduleID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *ScheduleID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i ScheduleID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
