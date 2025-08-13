package runtime

import (
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/nyaruka/gocommon/aws/cwatch"
)

type LLMTypeAndModel struct {
	Type  string
	Model string
}

type Stats struct {
	RealtimeTaskCount    map[string]int           // number of contact tasks handled by type
	RealtimeTaskErrors   map[string]int           // number of contact tasks that errored by type
	RealtimeTaskDuration map[string]time.Duration // total time spent handling contact tasks
	RealtimeTaskLatency  map[string]time.Duration // total time spent queuing and handling contact tasks
	RealtimeLockFails    int                      // number of times an attempt to get a contact lock failed

	CronTaskCount    map[string]int           // number of cron tasks run by type
	CronTaskDuration map[string]time.Duration // total time spent running cron tasks

	LLMCallCount    map[LLMTypeAndModel]int           // number of LLM calls run by type
	LLMCallDuration map[LLMTypeAndModel]time.Duration // total time spent making LLM calls

	WebhookCallCount    int           // number of webhook calls
	WebhookCallDuration time.Duration // total time spent handling webhook calls
}

func newStats() *Stats {
	return &Stats{
		RealtimeTaskCount:    make(map[string]int),
		RealtimeTaskErrors:   make(map[string]int),
		RealtimeTaskDuration: make(map[string]time.Duration),
		RealtimeTaskLatency:  make(map[string]time.Duration),

		CronTaskCount:    make(map[string]int),
		CronTaskDuration: make(map[string]time.Duration),

		LLMCallCount:    make(map[LLMTypeAndModel]int),
		LLMCallDuration: make(map[LLMTypeAndModel]time.Duration),
	}
}

func (s *Stats) ToMetrics(advanced bool) []types.MetricDatum {
	metrics := make([]types.MetricDatum, 0, 20)

	for typ, count := range s.RealtimeTaskCount {
		// convert task timings to averages
		avgDuration := s.RealtimeTaskDuration[typ] / time.Duration(count)
		avgLatency := s.RealtimeTaskLatency[typ] / time.Duration(count)

		metrics = append(metrics,
			cwatch.Datum("HandlerTaskCount", float64(count), types.StandardUnitCount, cwatch.Dimension("TaskType", typ)),
			cwatch.Datum("HandlerTaskErrors", float64(s.RealtimeTaskErrors[typ]), types.StandardUnitCount, cwatch.Dimension("TaskType", typ)),
			cwatch.Datum("HandlerTaskDuration", float64(avgDuration)/float64(time.Second), types.StandardUnitCount, cwatch.Dimension("TaskType", typ)),
			cwatch.Datum("HandlerTaskLatency", float64(avgLatency)/float64(time.Second), types.StandardUnitCount, cwatch.Dimension("TaskType", typ)),
		)
	}

	for typeAndModel, count := range s.LLMCallCount {
		avgTime := s.LLMCallDuration[typeAndModel] / time.Duration(count)

		metrics = append(metrics,
			cwatch.Datum("LLMCallCount", float64(count), types.StandardUnitCount, cwatch.Dimension("LLMType", typeAndModel.Type), cwatch.Dimension("LLMModel", typeAndModel.Model)),
			cwatch.Datum("LLMCallDuration", float64(avgTime)/float64(time.Second), types.StandardUnitSeconds, cwatch.Dimension("LLMType", typeAndModel.Type), cwatch.Dimension("LLMModel", typeAndModel.Model)),
		)
	}

	var avgWebhookDuration time.Duration
	if s.WebhookCallCount > 0 {
		avgWebhookDuration = s.WebhookCallDuration / time.Duration(s.WebhookCallCount)
	}

	metrics = append(metrics,
		cwatch.Datum("WebhookCallCount", float64(s.WebhookCallCount), types.StandardUnitCount),
		cwatch.Datum("WebhookCallDuration", float64(avgWebhookDuration)/float64(time.Second), types.StandardUnitSeconds),
	)

	if advanced {
		metrics = append(metrics,
			cwatch.Datum("HandlerLockFails", float64(s.RealtimeLockFails), types.StandardUnitCount),
		)

		for name, count := range s.CronTaskCount {
			avgTime := s.CronTaskDuration[name] / time.Duration(count)

			metrics = append(metrics,
				cwatch.Datum("CronTaskCount", float64(count), types.StandardUnitCount, cwatch.Dimension("TaskType", name)),
				cwatch.Datum("CronTaskDuration", float64(avgTime)/float64(time.Second), types.StandardUnitSeconds, cwatch.Dimension("TaskType", name)),
			)
		}
	}

	return metrics
}

// StatsCollector provides threadsafe stats collection
type StatsCollector struct {
	mutex sync.Mutex
	stats *Stats
}

// NewStatsCollector creates a new stats collector
func NewStatsCollector() *StatsCollector {
	return &StatsCollector{stats: newStats()}
}

func (c *StatsCollector) RecordRealtimeTask(typ string, d, l time.Duration, errored bool) {
	c.mutex.Lock()
	c.stats.RealtimeTaskCount[typ]++
	c.stats.RealtimeTaskDuration[typ] += d
	c.stats.RealtimeTaskLatency[typ] += l
	if errored {
		c.stats.RealtimeTaskErrors[typ]++
	}
	c.mutex.Unlock()
}

func (c *StatsCollector) RecordRealtimeLockFail() {
	c.mutex.Lock()
	c.stats.RealtimeLockFails++
	c.mutex.Unlock()
}

func (c *StatsCollector) RecordCronTask(name string, d time.Duration) {
	c.mutex.Lock()
	c.stats.CronTaskCount[name]++
	c.stats.CronTaskDuration[name] += d
	c.mutex.Unlock()
}

func (c *StatsCollector) RecordWebhookCall(d time.Duration) {
	c.mutex.Lock()
	c.stats.WebhookCallCount++
	c.stats.WebhookCallDuration += d
	c.mutex.Unlock()
}

func (c *StatsCollector) RecordLLMCall(typ, model string, d time.Duration) {
	c.mutex.Lock()
	c.stats.LLMCallCount[LLMTypeAndModel{typ, model}]++
	c.stats.LLMCallDuration[LLMTypeAndModel{typ, model}] += d
	c.mutex.Unlock()
}

// Extract returns the stats for the period since the last call
func (c *StatsCollector) Extract() *Stats {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	s := c.stats
	c.stats = newStats()
	return s
}
