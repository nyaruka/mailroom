package crons

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/vkutil/locks"
)

// Function is the function that will be called on our schedule
type Function func(context.Context, *runtime.Runtime) error

// Start calls the passed in function every interval, making sure it acquires a
// lock so that only one process is running at once. Note that across processes
// crons may be called more often than duration as there is no inter-process
// coordination of cron fires. (this might be a worthy addition)
func Start(rt *runtime.Runtime, wg *sync.WaitGroup, name string, allInstances bool, cronFunc Function, next func(time.Time) time.Time, timeout time.Duration, quit chan bool) {
	ctx := context.TODO()

	wg.Add(1) // add ourselves to the wait group

	lockName := fmt.Sprintf("lock:%s_lock", name) // for historical reasons...

	// for jobs that run on all instances, the lock key is specific to this instance
	if allInstances {
		lockName = fmt.Sprintf("%s:%s", lockName, rt.Config.InstanceID)
	}

	locker := locks.NewLocker(lockName, timeout+time.Second*30)

	wait := time.Duration(0)
	lastFire := time.Now()

	log := slog.With("cron", name)

	go func() {
		defer func() { wg.Done() }()

		for {
			select {
			case <-quit:
				// we are exiting, return so our goroutine can exit
				return

			case <-time.After(wait):
				lastFire = time.Now()

				// try to get lock but don't retry - if lock is taken then task is still running or running on another instance
				lock, err := locker.Grab(ctx, rt.VK, 0)
				if err != nil {
					break
				}

				if lock == "" {
					log.Debug("lock already present, sleeping")
					break
				}

				// ok, got the lock, run our cron function
				if err := fireCron(rt, cronFunc, timeout); err != nil {
					log.Error("error while running cron", "error", err)
				}

				// release our lock
				err = locker.Release(ctx, rt.VK, lock)
				if err != nil {
					log.Error("error releasing lock", "error", err)
				}
			}

			// calculate our next fire time
			nextFire := next(lastFire)
			wait = max(time.Until(nextFire), time.Duration(0))
		}
	}()
}

// fireCron is just a wrapper around the cron function we will call for the purposes of
// catching and logging panics
func fireCron(rt *runtime.Runtime, cronFunc Function, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	defer func() {
		// catch any panics and recover
		if panicVal := recover(); panicVal != nil {
			debug.PrintStack()

			sentry.CurrentHub().Recover(panicVal)
		}
	}()

	return cronFunc(ctx, rt)
}
