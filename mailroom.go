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
	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

const (
	appNodesRunningKey = "app-nodes:running"
)

// Mailroom is a service for handling RapidPro events
type Mailroom struct {
	ctx    context.Context
	cancel context.CancelFunc

	rt   *runtime.Runtime
	wg   *sync.WaitGroup
	quit chan bool

	realtimeForeman  *Foreman
	batchForeman     *Foreman
	throttledForeman *Foreman

	webserver *web.Server

	// both sqlx and valkey provide wait stats which are cummulative that we need to convert into increments by
	// tracking their previous values
	dbWaitDuration time.Duration
	vkWaitDuration time.Duration
}

// NewMailroom creates and returns a new mailroom instance
func NewMailroom(rt *runtime.Runtime) *Mailroom {
	mr := &Mailroom{
		rt:   rt,
		quit: make(chan bool),
		wg:   &sync.WaitGroup{},
	}
	mr.ctx, mr.cancel = context.WithCancel(context.Background())

	mr.realtimeForeman = NewForeman(mr.rt, mr.wg, mr.rt.Queues.Realtime, rt.Config.WorkersRealtime)
	mr.batchForeman = NewForeman(mr.rt, mr.wg, mr.rt.Queues.Batch, rt.Config.WorkersBatch)
	mr.throttledForeman = NewForeman(mr.rt, mr.wg, mr.rt.Queues.Throttled, rt.Config.WorkersThrottled)

	return mr
}

// Start starts the mailroom service
func (mr *Mailroom) Start() error {
	c := mr.rt.Config

	log := slog.With("comp", "mailroom")

	// test Postgres
	if err := checkDBConnection(mr.rt.DB.DB); err != nil {
		log.Error("postgres not reachable", "error", err)
	} else {
		log.Info("postgres ok")
	}
	if mr.rt.ReadonlyDB != mr.rt.DB.DB {
		if err := checkDBConnection(mr.rt.ReadonlyDB); err != nil {
			log.Error("readonly db not reachable", "error", err)
		} else {
			log.Info("readonly db ok")
		}
	} else {
		log.Warn("no distinct readonly db configured")
	}

	// test Valkey
	vc := mr.rt.VK.Get()
	defer vc.Close()
	if _, err := vc.Do("PING"); err != nil {
		log.Error("valkey not reachable", "error", err)
	} else {
		log.Info("valkey ok")
	}

	// test DynamoDB tables
	if err := dynamo.Test(mr.ctx, mr.rt.Dynamo, c.DynamoTablePrefix+"Main", c.DynamoTablePrefix+"History"); err != nil {
		log.Error("dynamodb not reachable", "error", err)
	} else {
		log.Info("dynamodb ok")
	}

	// test S3 buckets
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

	// test Elasticsearch
	ping, err := mr.rt.ES.Ping().Do(mr.ctx)
	if err != nil {
		log.Error("elasticsearch not available", "error", err)
	} else if !ping {
		log.Error("elasticsearch cluster not reachable")
	} else {
		log.Info("elastic ok")
	}

	if c.AndroidCredentialsFile != "" {
		mr.rt.FCM, err = fcm.NewClient(mr.ctx, fcm.WithCredentialsFile(c.AndroidCredentialsFile))
		if err != nil {
			log.Error("unable to create FCM client", "error", err)
		}
	} else {
		log.Warn("fcm not configured, no android syncing")
	}

	// init our foremen and start it
	mr.realtimeForeman.Start()
	mr.batchForeman.Start()
	mr.throttledForeman.Start()

	// start our web server
	mr.webserver = web.NewServer(mr.ctx, mr.rt, mr.wg)
	mr.webserver.Start()

	crons.StartAll(mr.rt, mr.wg, mr.quit)

	mr.startMetricsReporter(time.Minute)

	if err := mr.checkLastShutdown(mr.ctx); err != nil {
		return err
	}

	log.Info("mailroom started", "domain", c.Domain)

	return nil
}

func (mr *Mailroom) checkLastShutdown(ctx context.Context) error {
	nodeID := fmt.Sprintf("mailroom:%s", mr.rt.Config.InstanceID)
	vc := mr.rt.VK.Get()
	defer vc.Close()

	exists, err := redis.Bool(redis.DoContext(vc, ctx, "HEXISTS", appNodesRunningKey, nodeID))
	if err != nil {
		return fmt.Errorf("error checking last shutdown: %w", err)
	}

	if exists {
		slog.Error("mailroom node did not shutdown cleanly last time")
	} else {
		if _, err := redis.DoContext(vc, ctx, "HSET", appNodesRunningKey, nodeID, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("error setting app node state: %w", err)
		}
	}
	return nil
}

func (mr *Mailroom) recordShutdown(ctx context.Context) error {
	nodeID := fmt.Sprintf("mailroom:%s", mr.rt.Config.InstanceID)
	vc := mr.rt.VK.Get()
	defer vc.Close()

	if _, err := redis.DoContext(vc, ctx, "HDEL", appNodesRunningKey, nodeID); err != nil {
		return fmt.Errorf("error recording shutdown: %w", err)
	}
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

	realtimeSize, batchSize, throttledSize := getQueueSizes(ctx, mr.rt)

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
		cwatch.Datum("QueuedTasks", float64(realtimeSize), types.StandardUnitCount, cwatch.Dimension("QueueName", "realtime")),
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

	mr.realtimeForeman.Stop()
	mr.batchForeman.Stop()
	mr.throttledForeman.Stop()

	close(mr.quit) // tell workers and crons to stop
	mr.cancel()

	mr.webserver.Stop()

	mr.wg.Wait()

	if err := mr.recordShutdown(context.TODO()); err != nil {
		return fmt.Errorf("error recording shutdown: %w", err)
	}

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
