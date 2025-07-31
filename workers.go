package mailroom

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/queues"
)

// Foreman takes care of managing our set of workers and assigns msgs for each to send
type Foreman struct {
	rt               *runtime.Runtime
	wg               *sync.WaitGroup
	queue            queues.Fair
	workers          []*Worker
	availableWorkers chan *Worker
	quit             chan bool
}

// NewForeman creates a new Foreman for the passed in server with the number of max workers
func NewForeman(rt *runtime.Runtime, wg *sync.WaitGroup, q queues.Fair, maxWorkers int) *Foreman {
	foreman := &Foreman{
		rt:               rt,
		wg:               &sync.WaitGroup{},
		queue:            q,
		workers:          make([]*Worker, maxWorkers),
		availableWorkers: make(chan *Worker, maxWorkers),
		quit:             make(chan bool),
	}

	for i := range maxWorkers {
		foreman.workers[i] = NewWorker(foreman, fmt.Sprintf("%s-%d", q, i), wg)
	}

	return foreman
}

// Start starts the foreman and all its workers, assigning jobs while there are some
func (f *Foreman) Start() {
	for _, worker := range f.workers {
		worker.Start()
	}
	go f.Assign()
}

// Stop stops the foreman, waiting for assignment to finish. The workers notify on the main mailroom wait group when they are done.
func (f *Foreman) Stop() {
	// tell our assignment loop to stop
	close(f.quit)

	// wait for task assignment to finish before stopping workers so we don't try to assign new tasks to stopped workers
	f.wg.Wait()

	for _, worker := range f.workers {
		worker.Stop()
	}

	slog.Info("foreman stopped", "comp", "foreman", "queue", f.queue)
}

// Assign is our main loop for the Foreman, it takes care of popping the next outgoing task from our
// backend and assigning them to workers
func (f *Foreman) Assign() {
	f.wg.Add(1)
	defer f.wg.Done()
	log := slog.With("comp", "foreman", "queue", f.queue)

	log.Info("workers started and waiting", "workers", len(f.workers))

	lastSleep := false

	for {
		select {
		// return if we have been told to stop
		case <-f.quit:
			log.Info("foreman assignment stopped")
			return

		// otherwise, grab the next task and assign it to a worker
		case worker := <-f.availableWorkers:
			// see if we have a task to work on
			vc := f.rt.VK.Get()
			task, err := f.queue.Pop(context.TODO(), vc)
			vc.Close()

			if err == nil && task != nil {
				// if so, assign it to our worker
				worker.job <- task
				lastSleep = false
			} else {
				// we received an error getting the next message, log it
				if err != nil {
					log.Error("error popping task", "error", err)
				}

				// add our worker back to our queue and sleep a bit
				if !lastSleep {
					log.Debug("sleeping, no tasks")
					lastSleep = true
				}
				f.availableWorkers <- worker
				time.Sleep(250 * time.Millisecond)
			}
		}
	}
}

// Worker is our type for a single goroutine that is handling queued events
type Worker struct {
	foreman *Foreman
	id      string
	wg      *sync.WaitGroup

	job chan *queues.Task
}

// NewWorker creates a new worker responsible for working on events
func NewWorker(foreman *Foreman, id string, wg *sync.WaitGroup) *Worker {
	worker := &Worker{
		foreman: foreman,
		id:      id,
		wg:      wg,

		job: make(chan *queues.Task, 1),
	}
	return worker
}

// Start starts our Worker's goroutine and has it start waiting for tasks from the foreman
func (w *Worker) Start() {
	w.wg.Add(1)

	go func() {
		defer w.wg.Done()

		log := slog.With("worker_id", w.id)
		log.Debug("worker started")

		for {
			// list ourselves as available for work
			w.foreman.availableWorkers <- w

			// grab our next piece of work
			task := <-w.job

			// exit if we were stopped
			if task == nil {
				log.Info("worker stopped")
				return
			}

			w.handleTask(task)
		}
	}()
}

// Stop stops our worker
func (w *Worker) Stop() {
	close(w.job)
}

func (w *Worker) handleTask(task *queues.Task) {
	log := slog.With("worker_id", w.id, "task_id", task.ID, "task_type", task.Type, "org_id", task.OwnerID)

	defer func() {
		// catch any panics and recover
		if panicVal := recover(); panicVal != nil {
			debug.PrintStack()

			sentry.CurrentHub().Recover(panicVal)
		}

		// mark our task as complete
		vc := w.foreman.rt.VK.Get()
		err := w.foreman.queue.Done(context.TODO(), vc, task.OwnerID)
		if err != nil {
			log.Error("unable to mark task as complete", "error", err)
		}
		vc.Close()
	}()

	log.Info("task started")
	start := time.Now()

	if err := tasks.Perform(context.Background(), w.foreman.rt, task); err != nil {
		log.Error("error running task", "task", string(task.Task), "error", err)
	}

	elapsed := time.Since(start)
	log.Info("task complete", "elapsed", elapsed)

	// additionally if any task took longer than 1 minute, log as warning
	if elapsed > time.Minute {
		log.Warn("long running task", "task", string(task.Task), "elapsed", elapsed)
	}
}
