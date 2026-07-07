package mailroom

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/appleboy/go-fcm"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/mailroom/v26/core/crons"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

type Service struct {
	ctx    context.Context
	cancel context.CancelFunc

	rt        *runtime.Runtime
	workersWG *sync.WaitGroup
	quit      chan bool

	realtimeForeman  *Foreman
	batchForeman     *Foreman
	throttledForeman *Foreman

	webserver *web.Server

	// some stats are cummulative that we need to convert into increments by tracking their previous values
	dbWaitDuration time.Duration
	vkWaitDuration time.Duration
}

// NewService creates and returns a new mailroom service
func NewService(rt *runtime.Runtime) *Service {
	s := &Service{
		rt: rt,

		workersWG: &sync.WaitGroup{},
		quit:      make(chan bool),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	s.realtimeForeman = NewForeman(s.rt, s.rt.Queues.Realtime, rt.Config.WorkersRealtime)
	s.batchForeman = NewForeman(s.rt, s.rt.Queues.Batch, rt.Config.WorkersBatch)
	s.throttledForeman = NewForeman(s.rt, s.rt.Queues.Throttled, rt.Config.WorkersThrottled)

	return s
}

func (s *Service) Start() error {
	c := s.rt.Config

	log := slog.With("comp", "mailroom")

	// we can't function at all without Postgres and Valkey so unreachable is fatal, whereas writes to services
	// like DynamoDB and Elastic are spooled so we can start without them and recover later
	if err := checkDBConnection(s.rt.DB.DB); err != nil {
		return fmt.Errorf("postgres not reachable: %w", err)
	}
	log.Info("postgres ok")

	if s.rt.ReadonlyDB != s.rt.DB.DB {
		if err := checkDBConnection(s.rt.ReadonlyDB); err != nil {
			return fmt.Errorf("readonly db not reachable: %w", err)
		}
		log.Info("readonly db ok")
	} else {
		log.Warn("no distinct readonly db configured")
	}

	vc := s.rt.VK.Get()
	defer vc.Close()
	if _, err := vc.Do("PING"); err != nil {
		return fmt.Errorf("valkey not reachable: %w", err)
	}
	log.Info("valkey ok")

	// test DynamoDB tables
	if err := dynamo.Test(s.ctx, s.rt.Dynamo.Main.Client(), c.DynamoTablePrefix+"Main", c.DynamoTablePrefix+"History"); err != nil {
		log.Error("dynamodb not reachable", "error", err)
	} else {
		log.Info("dynamodb ok")
	}

	// test S3 bucket
	if err := s.rt.S3.Test(s.ctx, c.S3AttachmentsBucket); err != nil {
		log.Error("attachments bucket not accessible", "error", err)
	} else {
		log.Info("attachments bucket ok")
	}

	// test Elasticsearch
	ping, err := s.rt.ES.Client.Ping().Do(s.ctx)
	if err != nil {
		log.Error("elasticsearch not available", "error", err)
	} else if !ping {
		log.Error("elasticsearch cluster not reachable")
	} else {
		log.Info("elastic ok")
	}

	if c.AndroidCredentialsFile != "" {
		s.rt.FCM, err = fcm.NewClient(s.ctx, fcm.WithCredentialsFile(c.AndroidCredentialsFile))
		if err != nil {
			log.Error("unable to create FCM client", "error", err)
		}
	} else {
		log.Warn("fcm not configured, no android syncing")
	}

	// the Centrifugo client is built by the runtime; confirm here that the server is reachable and accepts our key
	if err := s.rt.Centrifugo.Client.Info(s.ctx); err != nil {
		log.Error("centrifugo not reachable", "error", err)
	} else {
		log.Info("centrifugo ok")
	}

	if err := s.rt.Start(); err != nil {
		return fmt.Errorf("error starting runtime: %w", err)
	} else {
		log.Info("runtime started")
	}

	// init our foremen and start it
	s.realtimeForeman.Start(s.workersWG)
	s.batchForeman.Start(s.workersWG)
	s.throttledForeman.Start(s.workersWG)

	// start our web server
	s.webserver = web.NewServer(s.ctx, s.rt, s.workersWG)
	s.webserver.Start()

	crons.StartAll(s.rt, s.workersWG, s.quit)

	s.startMetricsReporter(time.Minute)

	log.Info("mailroom started", "domain", c.Domain)

	return nil
}

func (s *Service) startMetricsReporter(interval time.Duration) {
	s.workersWG.Add(1)

	report := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		count, err := s.reportMetrics(ctx)
		cancel()
		if err != nil {
			slog.Error("error reporting metrics", "error", err)
		} else {
			slog.Info("sent metrics to cloudwatch", "count", count)
		}
	}

	go func() {
		defer func() {
			slog.Info("metrics reporter exiting")
			s.workersWG.Done()
		}()

		for {
			select {
			case <-s.quit:
				report()
				return
			case <-time.After(interval): // TODO align to half minute marks for queue sizes?
				report()
			}
		}
	}()
}

func (s *Service) reportMetrics(ctx context.Context) (int, error) {
	if s.rt.Config.MetricsReporting == "off" {
		return 0, nil
	}

	metrics := s.rt.Stats.Extract().ToMetrics(s.rt.Config.MetricsReporting == "advanced")

	realtimeSize, batchSize, throttledSize := getQueueSizes(ctx, s.rt)

	// calculate DB and valkey stats
	dbStats := s.rt.DB.Stats()
	vkStats := s.rt.VK.Stats()
	dbWaitDurationInPeriod := dbStats.WaitDuration - s.dbWaitDuration
	vkWaitDurationInPeriod := vkStats.WaitDuration - s.vkWaitDuration
	s.dbWaitDuration = dbStats.WaitDuration
	s.vkWaitDuration = vkStats.WaitDuration

	// instance level metrics are published without an instance dimension so that instances (which come and go with
	// deploys) are just samples of the same metric, and can be aggregated with statistics like Max and Sum
	metrics = append(metrics,
		cwatch.Datum("DBConnectionsInUse", float64(dbStats.InUse), types.StandardUnitCount),
		cwatch.Datum("DBConnectionWaitDuration", float64(dbWaitDurationInPeriod)/float64(time.Second), types.StandardUnitSeconds),
		cwatch.Datum("ValkeyConnectionsInUse", float64(vkStats.ActiveCount), types.StandardUnitCount),
		cwatch.Datum("ValkeyConnectionsWaitDuration", float64(vkWaitDurationInPeriod)/float64(time.Second), types.StandardUnitSeconds),
		cwatch.Datum("QueuedTasks", float64(realtimeSize), types.StandardUnitCount, cwatch.Dimension("QueueName", "realtime")),
		cwatch.Datum("QueuedTasks", float64(batchSize), types.StandardUnitCount, cwatch.Dimension("QueueName", "batch")),
		cwatch.Datum("QueuedTasks", float64(throttledSize), types.StandardUnitCount, cwatch.Dimension("QueueName", "throttled")),
		cwatch.Datum("DynamoSpooledItems", float64(s.rt.Dynamo.Spool.Size()), types.StandardUnitCount),
	)

	metrics = append(metrics,
		cwatch.Datum("ElasticSpooledItems", float64(s.rt.ES.Spool.Size()), types.StandardUnitCount),
	)

	if err := s.rt.CW.Send(ctx, metrics...); err != nil {
		return 0, fmt.Errorf("error sending metrics: %w", err)
	}

	return len(metrics), nil
}

// Stop stops the mailroom service
func (s *Service) Stop() error {
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
	return nil
}

func checkDBConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := db.PingContext(ctx)
	cancel()

	return err
}

func getQueueSizes(ctx context.Context, rt *runtime.Runtime) (int, int, int) {
	vc := rt.VK.Get()
	defer vc.Close()

	realtime, err := rt.Queues.Realtime.Size(ctx, vc)
	if err != nil {
		slog.Error("error calculating realtime queue size", "error", err)
	}
	batch, err := rt.Queues.Batch.Size(ctx, vc)
	if err != nil {
		slog.Error("error calculating batch queue size", "error", err)
	}
	throttled, err := rt.Queues.Throttled.Size(ctx, vc)
	if err != nil {
		slog.Error("error calculating throttled queue size", "error", err)
	}

	return realtime, batch, throttled
}
