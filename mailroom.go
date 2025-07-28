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
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/gocommon/aws/s3x"
	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
	"github.com/nyaruka/vkutil"
)

// Mailroom is a service for handling RapidPro events
type Mailroom struct {
	ctx    context.Context
	cancel context.CancelFunc

	rt   *runtime.Runtime
	wg   *sync.WaitGroup
	quit chan bool

	handlerForeman      *Foreman
	batchForeman        *Foreman
	throttledForeman    *Foreman
	throttledOldForeman *Foreman

	webserver *web.Server

	// both sqlx and valkey provide wait stats which are cummulative that we need to convert into increments by
	// tracking their previous values
	dbWaitDuration time.Duration
	vkWaitDuration time.Duration
}

// NewMailroom creates and returns a new mailroom instance
func NewMailroom(cfg *runtime.Config) *Mailroom {
	mr := &Mailroom{
		rt: &runtime.Runtime{
			Config: cfg,
			Queues: runtime.NewQueues(cfg),
			Stats:  runtime.NewStatsCollector(),
		},
		quit: make(chan bool),
		wg:   &sync.WaitGroup{},
	}
	mr.ctx, mr.cancel = context.WithCancel(context.Background())

	mr.handlerForeman = NewForeman(mr.rt, mr.wg, mr.rt.Queues.Handler, cfg.HandlerWorkers)
	mr.batchForeman = NewForeman(mr.rt, mr.wg, mr.rt.Queues.Batch, cfg.BatchWorkers)
	mr.throttledForeman = NewForeman(mr.rt, mr.wg, mr.rt.Queues.Throttled, cfg.BatchWorkers)
	mr.throttledOldForeman = NewForeman(mr.rt, mr.wg, mr.rt.Queues.ThrottledOld, cfg.BatchWorkers)

	return mr
}

// Start starts the mailroom service
func (mr *Mailroom) Start() error {
	c := mr.rt.Config

	log := slog.With("comp", "mailroom")

	var err error
	_, mr.rt.DB, err = openAndCheckDBConnection(c.DB, c.DBPoolSize)
	if err != nil {
		log.Error("db not reachable", "error", err)
	} else {
		log.Info("db ok")
	}

	if c.ReadonlyDB != "" {
		mr.rt.ReadonlyDB, _, err = openAndCheckDBConnection(c.ReadonlyDB, c.DBPoolSize)
		if err != nil {
			log.Error("readonly db not reachable", "error", err)
		} else {
			log.Info("readonly db ok")
		}
	} else {
		// if readonly DB not specified, just use default DB again
		mr.rt.ReadonlyDB = mr.rt.DB.DB
		log.Warn("no distinct readonly db configured")
	}

	mr.rt.VK, err = vkutil.NewPool(c.Valkey)
	if err != nil {
		log.Error("valkey not reachable", "error", err)
	} else {
		log.Info("valkey ok")
	}

	if c.AndroidCredentialsFile != "" {
		mr.rt.FCM, err = fcm.NewClient(mr.ctx, fcm.WithCredentialsFile(c.AndroidCredentialsFile))
		if err != nil {
			log.Error("unable to create FCM client", "error", err)
		}
	} else {
		log.Warn("fcm not configured, no android syncing")
	}

	// setup DynamoDB
	mr.rt.Dynamo, err = runtime.NewDynamoTables(c)
	if err != nil {
		return err
	}
	if err := mr.rt.Dynamo.Main.Test(mr.ctx); err != nil {
		log.Error("dynamodb not reachable", "error", err)
	} else {
		log.Info("dynamodb ok")
	}

	// setup S3 storage
	mr.rt.S3, err = s3x.NewService(c.AWSAccessKeyID, c.AWSSecretAccessKey, c.AWSRegion, c.S3Endpoint, c.S3Minio)
	if err != nil {
		return err
	}

	// check buckets
	if err := mr.rt.S3.Test(mr.ctx, c.S3AttachmentsBucket); err != nil {
		log.Error("attachments bucket not accessible", "error", err)
	} else {
		log.Info("attachments bucket ok")
	}
	if err := mr.rt.S3.Test(mr.ctx, c.S3SessionsBucket); err != nil {
		log.Error("sessions bucket not accessible", "error", err)
	} else {
		log.Info("sessions bucket ok")
	}

	// initialize our elastic client
	mr.rt.ES, err = elasticsearch.NewTypedClient(elasticsearch.Config{Addresses: []string{c.Elastic}, Username: c.ElasticUsername, Password: c.ElasticPassword})
	if err != nil {
		log.Error("elastic search not available", "error", err)
	} else {
		log.Info("elastic ok")
	}

	// configure and start cloudwatch
	mr.rt.CW, err = cwatch.NewService(c.AWSAccessKeyID, c.AWSSecretAccessKey, c.AWSRegion, c.CloudwatchNamespace, c.DeploymentID)
	if err != nil {
		log.Error("cloudwatch not available", "error", err)
	} else {
		log.Info("cloudwatch ok")
	}

	// init our foremen and start it
	mr.handlerForeman.Start()
	mr.batchForeman.Start()
	mr.throttledForeman.Start()
	mr.throttledOldForeman.Start()

	// start our web server
	mr.webserver = web.NewServer(mr.ctx, mr.rt, mr.wg)
	mr.webserver.Start()

	crons.StartAll(mr.rt, mr.wg, mr.quit)

	mr.startMetricsReporter(time.Minute)

	log.Info("mailroom started", "domain", c.Domain)

	return nil
}

func (mr *Mailroom) startMetricsReporter(interval time.Duration) {
	mr.wg.Add(1)

	report := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		count, err := mr.reportMetrics(ctx)
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
			mr.wg.Done()
		}()

		for {
			select {
			case <-mr.quit:
				report()
				return
			case <-time.After(interval): // TODO align to half minute marks for queue sizes?
				report()
			}
		}
	}()
}

func (mr *Mailroom) reportMetrics(ctx context.Context) (int, error) {
	metrics := mr.rt.Stats.Extract().ToMetrics()

	handlerSize, batchSize, throttledSize := getQueueSizes(ctx, mr.rt)

	// calculate DB and valkey stats
	dbStats := mr.rt.DB.Stats()
	vkStats := mr.rt.VK.Stats()
	dbWaitDurationInPeriod := dbStats.WaitDuration - mr.dbWaitDuration
	vkWaitDurationInPeriod := vkStats.WaitDuration - mr.vkWaitDuration
	mr.dbWaitDuration = dbStats.WaitDuration
	mr.vkWaitDuration = vkStats.WaitDuration

	hostDim := cwatch.Dimension("Host", mr.rt.Config.InstanceID)
	metrics = append(metrics,
		cwatch.Datum("DBConnectionsInUse", float64(dbStats.InUse), types.StandardUnitCount, hostDim),
		cwatch.Datum("DBConnectionWaitDuration", float64(dbWaitDurationInPeriod)/float64(time.Second), types.StandardUnitSeconds, hostDim),
		cwatch.Datum("ValkeyConnectionsInUse", float64(vkStats.ActiveCount), types.StandardUnitCount, hostDim),
		cwatch.Datum("ValkeyConnectionsWaitDuration", float64(vkWaitDurationInPeriod)/float64(time.Second), types.StandardUnitSeconds, hostDim),
		cwatch.Datum("QueuedTasks", float64(handlerSize), types.StandardUnitCount, cwatch.Dimension("QueueName", "handler")),
		cwatch.Datum("QueuedTasks", float64(batchSize), types.StandardUnitCount, cwatch.Dimension("QueueName", "batch")),
		cwatch.Datum("QueuedTasks", float64(throttledSize), types.StandardUnitCount, cwatch.Dimension("QueueName", "throttled")),
	)

	if err := mr.rt.CW.Send(ctx, metrics...); err != nil {
		return 0, fmt.Errorf("error sending metrics: %w", err)
	}

	return len(metrics), nil
}

// Stop stops the mailroom service
func (mr *Mailroom) Stop() error {
	log := slog.With("comp", "mailroom")
	log.Info("mailroom stopping")

	mr.handlerForeman.Stop()
	mr.batchForeman.Stop()
	mr.throttledForeman.Stop()
	mr.throttledOldForeman.Stop()

	close(mr.quit) // tell workers and crons to stop
	mr.cancel()

	mr.webserver.Stop()

	mr.wg.Wait()

	log.Info("mailroom stopped")
	return nil
}

func (mr *Mailroom) Runtime() *runtime.Runtime {
	return mr.rt
}

func openAndCheckDBConnection(url string, maxOpenConns int) (*sql.DB, *sqlx.DB, error) {
	db, err := sqlx.Open("postgres", url)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to open database connection: '%s': %w", url, err)
	}

	// configure our pool
	db.SetMaxIdleConns(8)
	db.SetMaxOpenConns(maxOpenConns)
	db.SetConnMaxLifetime(time.Minute * 30)

	// ping database...
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err = db.PingContext(ctx)
	cancel()

	return db.DB, db, err
}

func getQueueSizes(ctx context.Context, rt *runtime.Runtime) (int, int, int) {
	vc := rt.VK.Get()
	defer vc.Close()

	handler, err := rt.Queues.Handler.Size(ctx, vc)
	if err != nil {
		slog.Error("error calculating handler queue size", "error", err)
	}
	batch, err := rt.Queues.Batch.Size(ctx, vc)
	if err != nil {
		slog.Error("error calculating batch queue size", "error", err)
	}
	throttled, err := rt.Queues.Throttled.Size(ctx, vc)
	if err != nil {
		slog.Error("error calculating throttled queue size", "error", err)
	}

	return handler, batch, throttled
}
