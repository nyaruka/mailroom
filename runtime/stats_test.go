package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStats(t *testing.T) {
	sc := NewStatsCollector()
	sc.RecordCronTask("make_foos", 10*time.Second)
	sc.RecordCronTask("make_foos", 5*time.Second)
	sc.RecordLLMCall("openai", "gpt-4", 7*time.Second)
	sc.RecordLLMCall("openai", "gpt-4", 3*time.Second)
	sc.RecordLLMCall("anthropic", "claude-3.7", 4*time.Second)

	stats := sc.Extract()
	assert.Equal(t, 2, stats.CronTaskCount["make_foos"])
	assert.Equal(t, 15*time.Second, stats.CronTaskDuration["make_foos"])
	assert.Equal(t, 2, stats.LLMCallCount[LLMTypeAndModel{"openai", "gpt-4"}])
	assert.Equal(t, 10*time.Second, stats.LLMCallDuration[LLMTypeAndModel{"openai", "gpt-4"}])
	assert.Equal(t, 1, stats.LLMCallCount[LLMTypeAndModel{"anthropic", "claude-3.7"}])
	assert.Equal(t, 4*time.Second, stats.LLMCallDuration[LLMTypeAndModel{"anthropic", "claude-3.7"}])

	datums := stats.ToMetrics()
	assert.Len(t, datums, 9)
}
