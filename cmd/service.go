package cmd

import (
	ulog "log"
	"log/slog"
	"os"
	"os/signal"
	goruntime "runtime"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26"
	"github.com/nyaruka/mailroom/v26/runtime"
	slogmulti "github.com/samber/slog-multi"
	slogsentry "github.com/samber/slog-sentry/v2"
)

// Service starts the mailroom service, blocks until a termination signal is received, then stops it.
func Service(version, date string) error {
	cfg, err := runtime.LoadConfig()
	if err != nil {
		return err
	}
	cfg.Version = version

	// configure our logger
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	slog.SetDefault(slog.New(logHandler))

	// if we have a DSN entry, try to initialize it
	if cfg.SentryDSN != "" {
		err := sentry.Init(sentry.ClientOptions{Dsn: cfg.SentryDSN, ServerName: cfg.InstanceID, Release: version, AttachStacktrace: true})
		if err != nil {
			return err
		}

		defer sentry.Flush(2 * time.Second)

		slog.SetDefault(slog.New(
			slogmulti.Fanout(
				logHandler,
				slogsentry.Option{Level: slog.LevelError}.NewSentryHandler(),
			),
		))
	}

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

	svc := mailroom.NewService(rt)

	if err := svc.Start(); err != nil {
		return err
	}

	// handle our signals
	handleSignals(svc)
	return nil
}

// handleSignals takes care of trapping quit, interrupt or terminate signals and doing the right thing
func handleSignals(svc *mailroom.Service) {
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
			svc.Stop()
			return
		}
	}
}
