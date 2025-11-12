package crons

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/msgs"
	"github.com/nyaruka/mailroom/core/tasks/starts"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	Register("fire_schedules", &FireSchedulesCron{})
}

type FireSchedulesCron struct{}

func (c *FireSchedulesCron) Next(last time.Time) time.Time {
	return Next(last, time.Minute)
}

func (c *FireSchedulesCron) AllInstances() bool {
	return false
}

// checkSchedules looks up any expired schedules and fires them, setting the next fire as needed
func (c *FireSchedulesCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	// we sleep 1 second since we fire right on the minute and want to make sure to fire
	// things that are schedules right at the minute as well (and DB time may be slightly drifted)
	time.Sleep(time.Second * 1)

	log := slog.With("comp", "schedules_cron")

	// get any expired schedules
	unfired, err := models.GetUnfiredSchedules(ctx, rt.DB.DB)
	if err != nil {
		return nil, fmt.Errorf("error while getting unfired schedules: %w", err)
	}

	// for each unfired schedule
	broadcasts := 0
	triggers := 0
	noops := 0

	for _, s := range unfired {
		log := log.With("schedule_id", s.ID)
		now := time.Now()

		// calculate our next fire
		nextFire, err := s.GetNextFire(now)
		if err != nil {
			log.Error("error calculating next fire for schedule", "error", err)
			continue
		}

		// open a transaction for committing all the items for this fire
		tx, err := rt.DB.BeginTxx(ctx, nil)
		if err != nil {
			log.Error("error starting transaction for schedule fire", "error", err)
			continue
		}

		var task tasks.Task

		// if it is a broadcast
		if s.Broadcast != nil {
			log = log.With("broadcast_id", s.Broadcast.ID)

			// clone our broadcast, our schedule broadcast is just a template
			bcast, err := models.InsertChildBroadcast(ctx, tx, s.Broadcast)
			if err != nil {
				log.Error("error inserting new broadcast for schedule", "error", err)
				tx.Rollback()
				continue
			}

			// add our task to send this broadcast
			task = &msgs.SendBroadcastTask{Broadcast: bcast}
			broadcasts++

		} else if s.Trigger != nil {
			log = log.With("trigger_id", s.Trigger.ID())

			start := s.Trigger.CreateStart()

			// insert our flow start
			if err := models.InsertFlowStart(ctx, tx, start); err != nil {
				log.Error("error inserting new flow start for schedule", "error", err)
				tx.Rollback()
				continue
			}

			// add our flow start task
			task = &starts.StartFlowTask{FlowStart: start}
			triggers++
		} else {
			log.Error("schedule found with no associated active broadcast or trigger")
			noops++
		}

		if nextFire != nil {
			// update our next fire for this schedule
			err = s.UpdateFires(ctx, tx, now, nextFire)
			if err != nil {
				log.Error("error updating next fire for schedule", "error", err)
				tx.Rollback()
				continue
			}
		} else {
			// delete schedule and associated broadcast or trigger
			err = s.DeleteWithTarget(ctx, tx.Tx)
			if err != nil {
				log.Error("error deleting schedule", "error", err)
				tx.Rollback()
				continue
			}
		}

		// commit our transaction
		err = tx.Commit()
		if err != nil {
			log.Error("error committing schedule transaction", "error", err)
			tx.Rollback()
			continue
		}

		// add our task if we have one
		if task != nil {
			err = tasks.Queue(ctx, rt, rt.Queues.Batch, s.OrgID, task, true)
			if err != nil {
				log.Error(fmt.Sprintf("error queueing %s task from schedule", task.Type()), "error", err)
			}
		}
	}

	return map[string]any{"broadcasts": broadcasts, "triggers": triggers, "noops": noops}, nil
}
