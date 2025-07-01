package mailroom

import (
	"context"
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
	mrWG             *sync.WaitGroup // main mailroom wait group - workers added to this
	fmWG             *sync.WaitGroup // foreman assignment wait group so we can wait for assignment to finish
	queue            queues.Fair
	workers          []*Worker
	availableWorkers chan *Worker
	quit             chan bool
}

// NewForeman creates a new Foreman for the passed in server with the number of max workers
func NewForeman(rt *runtime.Runtime, wg *sync.WaitGroup, q queues.Fair, maxWorkers int) *Foreman {
	foreman := &Foreman{
		rt:               rt,
		mrWG:             wg,
		fmWG:             &sync.WaitGroup{},
		queue:            q,
		workers:          make([]*Worker, maxWorkers),
		availableWorkers: make(chan *Worker, maxWorkers),
		quit:             make(chan bool),
	}

	for i := range maxWorkers {
		foreman.workers[i] = NewWorker(foreman, i)
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

// Stop stops the foreman and all its workers, the wait group of the worker can be used to track progress
func (f *Foreman) Stop() {
	close(f.quit)

	// wait for foreman to finish assigning tasks before stopping workers
	f.fmWG.Wait()

	for _, worker := range f.workers {
		worker.Stop()
	}

	slog.Info("foreman stopping", "comp", "foreman", "queue", f.queue)
}

// Assign is our main loop for the Foreman, it takes care of popping the next outgoing task from our
// backend and assigning them to workers
func (f *Foreman) Assign() {
	f.fmWG.Add(1)
	defer f.fmWG.Done()
	log := slog.With("comp", "foreman", "queue", f.queue)

	log.Info("workers started and waiting", "workers", len(f.workers))

	lastSleep := false

	for {
		select {
		// return if we have been told to stop
		case <-f.quit:
			log.Info("foreman stopped")
			return

		// otherwise, grab the next task and assign it to a worker
		case worker := <-f.availableWorkers:
			// see if we have a task to work on
			rc := f.rt.RP.Get()
			task, err := f.queue.Pop(rc)
			rc.Close()

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
	id      int
	foreman *Foreman
	job     chan *queues.Task
}

// NewWorker creates a new worker responsible for working on events
func NewWorker(foreman *Foreman, id int) *Worker {
	worker := &Worker{
		id:      id,
		foreman: foreman,
		job:     make(chan *queues.Task, 1),
	}
	return worker
}

// Start starts our Worker's goroutine and has it start waiting for tasks from the foreman
func (w *Worker) Start() {
	w.foreman.mrWG.Add(1)

	go func() {
		defer w.foreman.mrWG.Done()

		log := slog.With("queue", w.foreman.queue, "worker_id", w.id)
		log.Debug("started")

		for {
			// list ourselves as available for work
			w.foreman.availableWorkers <- w

			// grab our next piece of work
			task := <-w.job

			// exit if we were stopped
			if task == nil {
				log.Debug("stopped")
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
	log := slog.With("queue", w.foreman.queue, "worker_id", w.id, "task_type", task.Type, "org_id", task.OwnerID)

	defer func() {
		// catch any panics and recover
		if panicVal := recover(); panicVal != nil {
			debug.PrintStack()

			sentry.CurrentHub().Recover(panicVal)
		}

		// mark our task as complete
		rc := w.foreman.rt.RP.Get()
		err := w.foreman.queue.Done(rc, task.OwnerID)
		if err != nil {
			log.Error("unable to mark task as complete", "error", err)
		}
		rc.Close()
	}()

	log.Debug("starting handling of task")
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
