package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/vkutil/locks"
)

// TypeIndexKnowledge is the type of the knowledge indexing task
const TypeIndexKnowledge = "index_knowledge"

const indexKnowledgeLockKey = "lock:knowledge:%s"

// how long we hold a source's lock for - has to outlive the task itself so that a worker still indexing keeps its
// exclusivity, and a worker that dies loses it without anything having to notice
const indexKnowledgeLockTTL = time.Hour

func init() {
	RegisterType(TypeIndexKnowledge, func() Task { return &IndexKnowledge{} })
}

// IndexKnowledge is our task to (re)index a knowledge source, queued by Django as an edit to its content commits
// and by the retry cron for sources left failed or stuck. The work is a delta on what's changed since we last
// indexed the source, so the task is idempotent and re-running it costs nothing when nothing has changed.
type IndexKnowledge struct {
	KnowledgeUUID models.KnowledgeUUID `json:"knowledge_uuid" validate:"required"`
}

func (t *IndexKnowledge) Type() string {
	return TypeIndexKnowledge
}

// Timeout has to stay comfortably below the interval after which the retry cron treats a source as stuck in
// indexing, so that a source still being worked on is never re-queued as abandoned.
func (t *IndexKnowledge) Timeout() time.Duration {
	return 30 * time.Minute
}

func (t *IndexKnowledge) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform implements tasks.Task
func (t *IndexKnowledge) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error {
	// one worker per source: rapid edits each queue a task and whichever gets the lock indexes for all of them,
	// the rest no-op. That can't lose the later edits - the winner takes its watermark before it reads, so
	// anything it didn't see leaves the source stale for the retry cron to re-queue.
	//
	// The lock is an efficiency guard rather than a correctness barrier, which is why valkey is enough: two
	// workers indexing the same source would each replace whole items' chunks in their own transaction and write
	// the same content, so the cost of losing a lock is duplicated embedding, not a corrupt index.
	locker := locks.NewLocker(fmt.Sprintf(indexKnowledgeLockKey, t.KnowledgeUUID), indexKnowledgeLockTTL)

	lock, err := locker.Grab(ctx, rt.VK, 0) // no waiting - whoever holds it is covering this edit too
	if err != nil {
		return fmt.Errorf("error grabbing lock to index knowledge source %s: %w", t.KnowledgeUUID, err)
	}
	if lock == "" {
		return nil
	}
	defer locker.Release(ctx, rt.VK, lock)

	k, err := models.GetKnowledge(ctx, rt.DB, oa.OrgID(), t.KnowledgeUUID)
	if err != nil {
		return fmt.Errorf("error loading knowledge source %s: %w", t.KnowledgeUUID, err)
	}

	// the source is gone or has been released.. nothing to index
	if k == nil {
		return nil
	}

	// a source of a type we can't index yet isn't an error - Django triggers for all of them
	if !slices.Contains(knowledge.IndexableTypes, k.Type) {
		return nil
	}

	if err := k.SetIndexing(ctx, rt.DB); err != nil {
		return fmt.Errorf("error marking knowledge source %d as indexing: %w", k.ID, err)
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
