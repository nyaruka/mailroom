package crons

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/crons"
)

const (
	statsExpires       = 60 * 60 * 48 // 2 days
	statsKeyBase       = "cron_stats"
	statsLastStartKey  = statsKeyBase + ":last_start"
	statsLastTimeKey   = statsKeyBase + ":last_time"
	statsLastResultKey = statsKeyBase + ":last_result"
	statsCallCountKey  = statsKeyBase + ":call_count"
	statsTotalTimeKey  = statsKeyBase + ":total_time"
)

var statsKeys = []string{
	statsLastStartKey,
	statsLastTimeKey,
	statsLastResultKey,
	statsCallCountKey,
	statsTotalTimeKey,
}

// Cron is a task to be repeated on a schedule
type Cron interface {
	// Next returns the next schedule time
	Next(time.Time) time.Time

	// Run performs the task
	Run(context.Context, *runtime.Runtime) (map[string]any, error)

	// AllInstances returns whether cron runs on all instances - i.e. locking is instance specific. This is for crons
	// like metrics which report instance specific stats. Other crons are synchronized across all instances.
	AllInstances() bool
}

var registeredCrons = map[string]Cron{}

// Register registers a new cron job
func Register(name string, c Cron) {
	registeredCrons[name] = c
}

// StartAll starts all registered cron jobs
func StartAll(rt *runtime.Runtime, wg *sync.WaitGroup, quit chan bool) {
	for name, c := range registeredCrons {
		crons.Start(rt, wg, name, c.AllInstances(), recordExecution(name, c.Run), c.Next, time.Minute*5, quit)
	}
}

func recordExecution(name string, r func(context.Context, *runtime.Runtime) (map[string]any, error)) func(context.Context, *runtime.Runtime) error {
	return func(ctx context.Context, rt *runtime.Runtime) error {
		log := slog.With("cron", name)
		started := time.Now()

		results, err := r(ctx, rt)

		elapsed := time.Since(started)
		elapsedSeconds := elapsed.Seconds()

		rt.Stats.RecordCronTask(name, elapsed)

		vc := rt.VK.Get()
		defer vc.Close()

		vc.Send("HSET", statsLastStartKey, name, started.Format(time.RFC3339))
		vc.Send("HSET", statsLastTimeKey, name, elapsedSeconds)
		vc.Send("HSET", statsLastResultKey, name, jsonx.MustMarshal(results))
		vc.Send("HINCRBY", statsCallCountKey, name, 1)
		vc.Send("HINCRBYFLOAT", statsTotalTimeKey, name, elapsedSeconds)
		for _, key := range statsKeys {
			vc.Send("EXPIRE", key, statsExpires)
		}

		if err := vc.Flush(); err != nil {
			log.Error("error writing cron results to valkey", "error", err)
		}

		logResults := make([]any, 0, len(results)*2)
		for k, v := range results {
			logResults = append(logResults, k, v)
		}
		log = log.With("elapsed", elapsed, slog.Group("results", logResults...))

		// if cron too longer than a minute, log as error
		if elapsed > time.Minute {
			log.Error("cron took too long")
		} else {
			log.Info("cron completed")
		}

		return err
	}
}

// Next returns the next time we should fire based on the passed in time and interval
func Next(last time.Time, interval time.Duration) time.Time {
	if interval >= time.Second && interval < time.Minute {
		return last.Add(interval - ((time.Duration(last.Second()) * time.Second) % interval))
	} else if interval == time.Minute {
		seconds := time.Duration(60-last.Second()) + 1
		return last.Add(seconds * time.Second)
	} else {
		// no special treatment for other things
		return last.Add(interval)
	}
}
