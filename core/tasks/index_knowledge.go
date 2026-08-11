package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// TypeIndexKnowledge is the type of the knowledge indexing task
const TypeIndexKnowledge = "index_knowledge"

func init() {
	RegisterType(TypeIndexKnowledge, func() Task { return &IndexKnowledge{} })
}

// IndexKnowledge is our task to (re)index a knowledge source, queued by Django as an edit to its content commits
// and by the recovery cron for sources left failed or stuck. The work is a delta on what's changed since we last
// indexed the source, so the task is idempotent and re-running it costs nothing when nothing has changed.
type IndexKnowledge struct {
	KnowledgeUUID models.KnowledgeUUID `json:"knowledge_uuid" validate:"required"`
}

func (t *IndexKnowledge) Type() string {
	return TypeIndexKnowledge
}

// Timeout has to stay comfortably below the interval after which the recovery sweep treats a source as stuck in
// indexing, so that a source still being worked on is never re-queued as abandoned.
func (t *IndexKnowledge) Timeout() time.Duration {
	return 30 * time.Minute
}

func (t *IndexKnowledge) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform implements tasks.Task
func (t *IndexKnowledge) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error {
	// no embeddings service configured means knowledge indexing is disabled
	if rt.Config.EmbeddingsEndpoint == "" {
		return nil
	}

	k, err := models.ClaimKnowledge(ctx, rt.DB, oa.OrgID(), t.KnowledgeUUID, knowledge.IndexableTypes)
	if err != nil {
		return fmt.Errorf("error claiming knowledge source %s: %w", t.KnowledgeUUID, err)
	}

	// the source is gone, released, not a type we can index, or already being indexed - all no-ops. Rapid edits
	// collapsing into the one run this way can't lose the later ones: that run took its watermark before it read,
	// so anything it didn't see leaves the source stale for the recovery sweep to re-queue.
	if k == nil {
		return nil
	}

	if err := knowledge.IndexSource(ctx, rt, k); err != nil {
		// the failure has to land in the database - and with a context detached from the task's so that a timeout
		// during indexing can't also prevent us recording the outcome
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()

		if ferr := k.SetFailed(fctx, rt.DB, err.Error()); ferr != nil {
			slog.Error("error marking knowledge source as failed", "error", ferr, "knowledge_id", k.ID)
		}

		return fmt.Errorf("error indexing knowledge source %d: %w", k.ID, err)
	}

	return nil
}
