package crons

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	Register("retry_knowledge_indexing", &RetryKnowledgeIndexingCron{BatchSize: 25})
}

// RetryKnowledgeIndexingCron is the recovery backstop for knowledge indexing, not its normal trigger: Django calls
// /mi/knowledge/index as an edit commits and that queues the indexing task, so indexing starts within seconds of a
// change rather than on the next sweep. But batch tasks aren't retried, so a task that errors leaves its source
// failed and a worker that dies mid-index leaves it stuck indexing, with nothing else to revive either - and a
// trigger lost between Django and the queue would never be noticed at all. This sweep re-queues all of those, which
// is why it can run slowly.
type RetryKnowledgeIndexingCron struct {
	BatchSize int // the maximum number of sources to queue per run
}

func (c *RetryKnowledgeIndexingCron) Next(last time.Time) time.Time {
	return Next(last, 5*time.Minute)
}

func (c *RetryKnowledgeIndexingCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	stale, err := models.GetStaleKnowledge(ctx, rt.DB, knowledge.IndexableTypes, c.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("error getting stale knowledge sources: %w", err)
	}

	for _, k := range stale {
		// queued without priority, unlike the endpoint's tasks - nobody is waiting on a recovery
		if err := tasks.Queue(ctx, rt, rt.Queues.Batch, k.OrgID, &tasks.IndexKnowledge{KnowledgeUUID: k.UUID}, false); err != nil {
			return nil, fmt.Errorf("error queueing index knowledge task for source %d: %w", k.ID, err)
		}
	}

	return map[string]any{"queued": len(stale)}, nil
}
