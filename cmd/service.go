package cmd

import (
	"context"
	"fmt"
	ulog "log"
	"log/slog"
	"os"
	"os/signal"
	goruntime "runtime"
	"sync"
	"syscall"
	"time"

	"github.com/appleboy/go-fcm"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/worker"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/services/embeddings/intfloat"
	"github.com/nyaruka/mailroom/v26/web"
)

// Service starts the mailroom service, blocks until a termination signal is received, then stops it. Configuration
// is loaded on top of the given defaults, e.g. runtime.NewDefaultConfig(). All logging is sent to the given handler,
// e.g. LogHandler(), whose level is set from the loaded config.
func Service(defaults *runtime.Config, version, date string, logHandler slog.Handler) error {
	cfg, err := runtime.LoadConfig(defaults, os.Args[1:])
	if err != nil {
		return err
	}
	cfg.Version = version

	// configure our logger
	logLevel.Set(cfg.LogLevel)
	slog.SetDefault(slog.New(logHandler))

	log := slog.With("comp", "main")
	log.Info("starting mailroom", "version", version, "released", date)

	if cfg.UUIDSeed != 0 {
		uuids.SetGenerator(uuids.NewSeededGenerator(int64(cfg.UUIDSeed), time.Now))
		log.Warn("using seeded UUID generation", "uuid-seed", cfg.UUIDSeed)
	}

	rt, err := runtime.NewRuntime(cfg)
	if err != nil {
		return err
	}

	svc, err := startService(rt)
	if err != nil {
		return err
	}

	handleSignals(svc)

	return nil
}

// service is the set of components this process runs, in the order they're started
type service struct {
	ctx    context.Context
	cancel context.CancelFunc

	rt        *runtime.Runtime
	workersWG *sync.WaitGroup
	quit      chan bool

	realtimeForeman  *worker.Foreman
	batchForeman     *worker.Foreman
	throttledForeman *worker.Foreman

	webserver *web.Server

	// some stats are cummulative that we need to convert into increments by tracking their previous values
	dbWaitDuration time.Duration
	vkWaitDuration time.Duration
}

// startService starts each component in turn. Everything which can fail does so before anything has been started,
// so a failure part way through can't leave the process with half a runtime.
func startService(rt *runtime.Runtime) (*service, error) {
	s := &service{rt: rt, workersWG: &sync.WaitGroup{}, quit: make(chan bool)}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	c := rt.Config
	log := slog.With("comp", "mailroom")

	// services which every deployment has, and which the runtime doesn't build for itself
	rt.Embeddings = intfloat.NewService(rt.HTTP.Services, c.EmbeddingsEndpoint, c.EmbeddingsModel)

	// log what we can and can't reach before we start doing anything with it
	if err := testConnections(s.ctx, rt); err != nil {
		return nil, err
	}

	// create the services which are only enabled in some deployments - a failure to create one leaves it nil, which
	// is how callers know the feature is unavailable, so it must not be assigned to on the error path
	if c.AndroidCredentialsFile != "" {
		fcmClient, err := fcm.NewClient(s.ctx, fcm.WithCredentialsFile(c.AndroidCredentialsFile))
		if err != nil {
			log.Error("unable to create FCM client", "error", err)
		} else {
			rt.FCM = fcmClient
		}
	} else {
		log.Warn("fcm not configured, no android syncing")
	}

	if err := rt.Start(); err != nil {
		return nil, fmt.Errorf("error starting runtime: %w", err)
	}
	log.Info("runtime started")

	models.InitCache(rt)

	// init our foremen and start them
	s.realtimeForeman = worker.NewForeman(rt, rt.Queues.Realtime, c.WorkersRealtime)
	s.batchForeman = worker.NewForeman(rt, rt.Queues.Batch, c.WorkersBatch)
	s.throttledForeman = worker.NewForeman(rt, rt.Queues.Throttled, c.WorkersThrottled)
	s.realtimeForeman.Start(s.workersWG)
	s.batchForeman.Start(s.workersWG)
	s.throttledForeman.Start(s.workersWG)

	// start our web server
	s.webserver = web.NewServer(s.ctx, rt, s.workersWG)
	s.webserver.Start()

	crons.StartAll(rt, s.workersWG, s.quit)

	s.startMetricsReporter(time.Minute)

	log.Info("mailroom started", "domain", c.Domain)

	return s, nil
}

// stop stops each component in the reverse of the order it was started
func (s *service) stop() {
	log := slog.With("comp", "mailroom")
	log.Info("mailroom stopping")

	s.realtimeForeman.Stop()
	s.batchForeman.Stop()
	s.throttledForeman.Stop()

	close(s.quit) // tell workers and crons to stop
	s.cancel()

	s.webserver.Stop()

	s.workersWG.Wait()

	log.Info("workers stopped")

	s.rt.Stop()

	log.Info("runtime stopped")

	log.Info("mailroom stopped")
}

// handleSignals takes care of trapping quit, interrupt or terminate signals and doing the right thing
func handleSignals(svc *service) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	for {
		sig := <-sigs
		log := slog.With("comp", "main", "signal", sig)

		switch sig {
		case syscall.SIGQUIT:
			buf := make([]byte, 1<<20)
			stacklen := goruntime.Stack(buf, true)
			log.Info("received quit signal, dumping stack")
			ulog.Printf("\n%s", buf[:stacklen])
		case syscall.SIGINT, syscall.SIGTERM:
			log.Info("received exit signal, exiting")
			svc.stop()
			return
		}
	}
}
