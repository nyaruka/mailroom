package mailroom_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/mailroom"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/utils/queues"
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

	fm := mailroom.NewForeman(rt, wg, q, 2)
	fm.Start()

	// wait for queue to empty
	for {
		if size, err := q.Size(ctx, vc); err != nil || size == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// give workers time to finish the last task and mark done
	time.Sleep(150 * time.Millisecond)

	assertvk.ZGetAll(t, vc, "{test}:queued", map[string]float64{})
	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{})

	// queue more tasks and immediately stop the foreman
	for range 10 {
		q.Push(ctx, vc, "test", 1, &testTask{}, false)
	}

	// give workers time to pick up tasks
	time.Sleep(300 * time.Millisecond)

	fm.Stop()

	wg.Wait()

	assertvk.ZGetAll(t, vc, "{test}:active", map[string]float64{})
}
