package crons

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	Register("index_knowledge", &IndexKnowledgeCron{BatchSize: 25})
}

// IndexKnowledgeCron sweeps for knowledge sources needing (re)indexing and indexes them. Sources are claimed with
// FOR UPDATE SKIP LOCKED so concurrent sweeps can't double index, and because there is no task retry, every source
// claimed ends the run as either ready or failed - never stuck in indexing.
type IndexKnowledgeCron struct {
	BatchSize int // the maximum number of sources to claim and index per run
}

func (c *IndexKnowledgeCron) Next(last time.Time) time.Time {
	return Next(last, time.Minute)
}

func (c *IndexKnowledgeCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	// no embeddings service configured means knowledge indexing is disabled
	if rt.Config.EmbeddingsEndpoint == "" {
		return map[string]any{"indexed": 0, "failed": 0}, nil
	}

	log := slog.With("comp", "index_knowledge")

	claimed, err := models.ClaimStaleKnowledge(ctx, rt.DB, knowledge.IndexableTypes, c.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("error claiming stale knowledge sources: %w", err)
	}

	indexed, failed := 0, 0

	for _, k := range claimed {
		if err := knowledge.IndexSource(ctx, rt, k); err != nil {
			log.Error("error indexing knowledge source", "error", err, "knowledge_id", k.ID, "org_id", k.OrgID)

			// the failure has to land in the database - and with a context detached from the cron's so that a
			// timeout during indexing can't also prevent us recording the outcome
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			if ferr := k.SetFailed(fctx, rt.DB, err.Error()); ferr != nil {
				log.Error("error marking knowledge source as failed", "error", ferr, "knowledge_id", k.ID)
			}
			cancel()

			failed++
			continue
		}

		indexed++
	}

	return map[string]any{"indexed": indexed, "failed": failed}, nil
}
