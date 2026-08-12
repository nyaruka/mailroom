package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/nyaruka/gocommon/aws/cwatch"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// startMetricsReporter reports our metrics to cloudwatch on the given interval until the service is stopped
func (s *service) startMetricsReporter(interval time.Duration) {
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

func (s *service) reportMetrics(ctx context.Context) (int, error) {
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
