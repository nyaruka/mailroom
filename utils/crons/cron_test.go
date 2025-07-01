package crons_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/utils/crons"
	"github.com/stretchr/testify/assert"
)

func TestCron(t *testing.T) {
	_, rt := testsuite.Runtime()
	rc := rt.VK.Get()
	defer rc.Close()

	align := func() {
		untilNextSecond := time.Nanosecond * time.Duration(1_000_000_000-time.Now().Nanosecond()) // time until next second boundary
		time.Sleep(untilNextSecond)                                                               // wait until after second boundary
	}

	createCronFunc := func(running *bool, fired *int, delays map[int]time.Duration, defaultDelay time.Duration) crons.Function {
		return func(ctx context.Context, rt *runtime.Runtime) error {
			if *running {
				assert.Fail(t, "more than 1 thread is trying to run our cron job")
			}

			*running = true
			delay := delays[*fired]
			if delay == 0 {
				delay = defaultDelay
			}
			time.Sleep(delay)
			*fired++
			*running = false
			return nil
		}
	}

	fired := 0
	wg := &sync.WaitGroup{}
	quit := make(chan bool)
	running := false

	align()

	next := func(last time.Time) time.Time {
		interval := time.Millisecond * 250
		return last.Add(interval - ((time.Duration(last.Second()) * time.Second) % interval))
	}

	// start a job that takes ~100 ms and runs every 250ms
	crons.Start(rt, wg, "test1", false, createCronFunc(&running, &fired, map[int]time.Duration{}, time.Millisecond*100), next, time.Minute, quit)

	// wait a bit, should only have fired three times (initial time + three repeats)
	time.Sleep(time.Millisecond * 875) // time for 3 delays between tasks plus half of another delay
	assert.Equal(t, 4, fired)

	// tell the job to quit and check we don't see more fires
	close(quit)

	time.Sleep(time.Millisecond * 500)
	assert.Equal(t, 4, fired)

	fired = 0
	quit = make(chan bool)
	running = false

	align()

	// simulate the job taking 400ms to run on the second fire, thus skipping the third fire
	crons.Start(rt, wg, "test2", false, createCronFunc(&running, &fired, map[int]time.Duration{1: time.Millisecond * 400}, time.Millisecond*100), next, time.Minute, quit)

	time.Sleep(time.Millisecond * 875)
	assert.Equal(t, 3, fired)

	close(quit)

	// simulate two different instances running the same cron
	cfg1 := *rt.Config
	cfg2 := *rt.Config
	cfg1.InstanceID = "instance1"
	cfg2.InstanceID = "instance2"
	rt1 := *rt
	rt1.Config = &cfg1
	rt2 := *rt
	rt2.Config = &cfg2

	fired1 := 0
	fired2 := 0
	quit = make(chan bool)
	running = false

	align()

	crons.Start(&rt1, wg, "test3", false, createCronFunc(&running, &fired1, map[int]time.Duration{}, time.Millisecond*100), next, time.Minute, quit)
	crons.Start(&rt2, wg, "test3", false, createCronFunc(&running, &fired2, map[int]time.Duration{}, time.Millisecond*100), next, time.Minute, quit)

	// same number of fires as if only a single instance was running it...
	time.Sleep(time.Millisecond * 875)
	assert.Equal(t, 4, fired1+fired2) // can't say which instances will run the 4 fires

	close(quit)

	fired1 = 0
	fired2 = 0
	quit = make(chan bool)
	running1 := false
	running2 := false

	align()

	// unless we start the cron with allInstances = true
	crons.Start(&rt1, wg, "test4", true, createCronFunc(&running1, &fired1, map[int]time.Duration{}, time.Millisecond*100), next, time.Minute, quit)
	crons.Start(&rt2, wg, "test4", true, createCronFunc(&running2, &fired2, map[int]time.Duration{}, time.Millisecond*100), next, time.Minute, quit)

	// now both instances fire 4 times
	time.Sleep(time.Millisecond * 875)
	assert.Equal(t, 4, fired1)
	assert.Equal(t, 4, fired2)

	close(quit)
}
