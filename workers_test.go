package mailroom_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/utils/queues"
	"github.com/nyaruka/vkutil/assertvk"
)

type testTask struct{}

func (t *testTask) Type() string               { return "test" }
func (t *testTask) Timeout() time.Duration     { return 5 * time.Second }
func (t *testTask) WithAssets() models.Refresh { return models.RefreshNone }
func (t *testTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	time.Sleep(100 * time.Millisecond)
	return nil
}

func TestForemanAndWorkers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	wg := &sync.WaitGroup{}
	q := queues.NewFair("test", 10)

	vc := rt.VK.Get()
	defer vc.Close()

	tasks.RegisterType("test", func() tasks.Task { return &testTask{} })

	// queue up tasks of unknown type to ensure it doesn't break further processing
	q.Push(ctx, vc, "spam", 1, "argh", false)
	q.Push(ctx, vc, "spam", 2, "argh", false)

	// queue up 5 tasks for two orgs
	for range 5 {
		q.Push(ctx, vc, "test", 1, &testTask{}, false)
	}
	for range 5 {
		q.Push(ctx, vc, "test", 2, &testTask{}, false)
	}

	fm := mailroom.NewForeman(rt, q, 2)
	fm.Start(wg)

	// wait for queue to empty
	for {
		if size, err := q.Size(ctx, vc); err != nil || size == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// give workers time to finish the last task and mark done
	time.Sleep(150 * time.Millisecond)

	assertvk.ZGetAll(t, vc, "{tasks:test}:queued", map[string]float64{})
	assertvk.ZGetAll(t, vc, "{tasks:test}:active", map[string]float64{})

	// queue more tasks and immediately stop the foreman
	for range 10 {
		q.Push(ctx, vc, "test", 1, &testTask{}, false)
	}

	// give workers time to pick up tasks
	time.Sleep(300 * time.Millisecond)

	fm.Stop()

	wg.Wait()

	assertvk.ZGetAll(t, vc, "{tasks:test}:active", map[string]float64{})
}

func TestForemanWithZeroWorkers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	wg := &sync.WaitGroup{}
	q := queues.NewFair("test", 0)

	vc := rt.VK.Get()
	defer vc.Close()

	tasks.RegisterType("test", func() tasks.Task { return &testTask{} })

	// queue a task - it should not be processed because there are no workers
	q.Push(ctx, vc, "test", 1, &testTask{}, false)

	fm := mailroom.NewForeman(rt, q, 0)
	fm.Start(wg) // must not spawn an Assign goroutine
	fm.Stop()    // must not block waiting on a goroutine that was never started

	wg.Wait() // returns immediately

	// task should still be queued, never popped
	size, err := q.Size(ctx, vc)
	if err != nil {
		t.Fatalf("error getting queue size: %v", err)
	}
	if size != 1 {
		t.Fatalf("expected queue size 1, got %d", size)
	}
}
