package main

import (
	ulog "log"
	"log/slog"
	"os"
	"os/signal"
	goruntime "runtime"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	_ "github.com/lib/pq"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom"
	"github.com/nyaruka/mailroom/runtime"
	slogmulti "github.com/samber/slog-multi"
	slogsentry "github.com/samber/slog-sentry/v2"

	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	_ "github.com/nyaruka/mailroom/core/runner/hooks"
	_ "github.com/nyaruka/mailroom/core/tasks/campaigns"
	_ "github.com/nyaruka/mailroom/core/tasks/contacts"
	_ "github.com/nyaruka/mailroom/core/tasks/handler"
	_ "github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	_ "github.com/nyaruka/mailroom/core/tasks/interrupts"
	_ "github.com/nyaruka/mailroom/core/tasks/msgs"
	_ "github.com/nyaruka/mailroom/core/tasks/starts"
	_ "github.com/nyaruka/mailroom/services/airtime/dtone"
	_ "github.com/nyaruka/mailroom/services/ivr/bandwidth"
	_ "github.com/nyaruka/mailroom/services/ivr/twiml"
	_ "github.com/nyaruka/mailroom/services/ivr/vonage"
	_ "github.com/nyaruka/mailroom/services/llm/anthropic"
	_ "github.com/nyaruka/mailroom/services/llm/deepseek"
	_ "github.com/nyaruka/mailroom/services/llm/google"
	_ "github.com/nyaruka/mailroom/services/llm/openai"
	_ "github.com/nyaruka/mailroom/services/llm/openai_azure"
	_ "github.com/nyaruka/mailroom/web/android"
	_ "github.com/nyaruka/mailroom/web/campaign"
	_ "github.com/nyaruka/mailroom/web/contact"
	_ "github.com/nyaruka/mailroom/web/docs"
	_ "github.com/nyaruka/mailroom/web/flow"
	_ "github.com/nyaruka/mailroom/web/ivr"
	_ "github.com/nyaruka/mailroom/web/llm"
	_ "github.com/nyaruka/mailroom/web/msg"
	_ "github.com/nyaruka/mailroom/web/org"
	_ "github.com/nyaruka/mailroom/web/po"
	_ "github.com/nyaruka/mailroom/web/simulation"
	_ "github.com/nyaruka/mailroom/web/ticket"
)

var (
	// https://goreleaser.com/cookbooks/using-main.version
	version = "dev"
	date    = "unknown"
)

func main() {
	config := runtime.LoadConfig()
	config.Version = version

	// configure our logger
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: config.LogLevel})
	slog.SetDefault(slog.New(logHandler))

	// if we have a DSN entry, try to initialize it
	if config.SentryDSN != "" {
		err := sentry.Init(sentry.ClientOptions{Dsn: config.SentryDSN, ServerName: config.InstanceID, Release: version, AttachStacktrace: true})
		if err != nil {
			slog.Error("error initiating sentry client", "error", err, "dsn", config.SentryDSN)
			os.Exit(1)
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

	if config.UUIDSeed != 0 {
		uuids.SetGenerator(uuids.NewSeededGenerator(int64(config.UUIDSeed), time.Now))
		log.Warn("using seeded UUID generation", "uuid-seed", config.UUIDSeed)
	}

	mr := mailroom.NewMailroom(config)
	err := mr.Start()
	if err != nil {
		log.Error("unable to start server", "error", err)
		os.Exit(1)
	}

	// handle our signals
	handleSignals(mr)
}

// handleSignals takes care of trapping quit, interrupt or terminate signals and doing the right thing
func handleSignals(mr *mailroom.Mailroom) {
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
			mr.Stop()
			return
		}
	}
}
