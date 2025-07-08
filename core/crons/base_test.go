package crons_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
)

func TestNext(t *testing.T) {
	tcs := []struct {
		last     time.Time
		interval time.Duration
		expected time.Time
	}{
		{time.Date(2000, 1, 1, 1, 1, 4, 0, time.UTC), time.Minute, time.Date(2000, 1, 1, 1, 2, 1, 0, time.UTC)},
		{time.Date(2000, 1, 1, 1, 1, 44, 0, time.UTC), time.Minute, time.Date(2000, 1, 1, 1, 2, 1, 0, time.UTC)},
		{time.Date(2000, 1, 1, 1, 1, 1, 100, time.UTC), time.Millisecond * 150, time.Date(2000, 1, 1, 1, 1, 1, 150000100, time.UTC)},
		{time.Date(2000, 1, 1, 2, 6, 1, 0, time.UTC), time.Minute * 10, time.Date(2000, 1, 1, 2, 16, 1, 0, time.UTC)},
		{time.Date(2000, 1, 1, 1, 1, 4, 0, time.UTC), time.Second * 15, time.Date(2000, 1, 1, 1, 1, 15, 0, time.UTC)},
	}

	for _, tc := range tcs {
		actual := crons.Next(tc.last, tc.interval)
		assert.Equal(t, tc.expected, actual, "next fire mismatch for %s + %s", tc.last, tc.interval)
	}
}

type TestCron struct {
	ran bool
}

func (c *TestCron) Next(last time.Time) time.Time {
	return crons.Next(last, time.Minute*5)
}

func (c *TestCron) AllInstances() bool {
	return false
}

func (c *TestCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	c.ran = true
	return map[string]any{"foo": 123}, nil
}

func TestStats(t *testing.T) {
	_, rt := testsuite.Runtime()
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(testsuite.ResetValkey)

	cron := &TestCron{}
	crons.Register("test1", cron)

	wg := &sync.WaitGroup{}
	quit := make(chan bool)

	crons.StartAll(rt, wg, quit)

	time.Sleep(time.Millisecond * 100)

	assertvk.Exists(t, vc, "cron_stats:last_start")
	assertvk.Exists(t, vc, "cron_stats:last_time")
	assertvk.HGet(t, vc, "cron_stats:last_result", "test1", `{"foo":123}`)
	assertvk.HGet(t, vc, "cron_stats:call_count", "test1", "1")
	assertvk.Exists(t, vc, "cron_stats:total_time")

	close(quit)
}
