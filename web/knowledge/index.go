package knowledge

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/knowledge/index", web.JSONPayload(handleIndex))
}

// Triggers (re)indexing of a knowledge source in a task.
//
//	{
//	  "org_id": 1,
//	  "knowledge_uuid": "97180291-8d95-4a6b-8a1a-63c44bb84b77"
//	}
type indexRequest struct {
	OrgID         models.OrgID         `json:"org_id"         validate:"required"`
	KnowledgeUUID models.KnowledgeUUID `json:"knowledge_uuid" validate:"required"`
}

// handles a request to index a knowledge source. Indexing happens in a task rather than inline because chunking and
// embedding a source is slow and unbounded by its content - so the caller isn't kept waiting on it, and large
// sources scale with the batch workers. The task claims the source itself, so callers can trigger freely: rapid
// edits collapse into a single indexing run.
//
// Callers must trigger on commit of the edit rather than during it. The indexer reads what's changed since the
// source was last indexed, so a task that ran while the edit was still uncommitted would advance that watermark
// past it - and the watermark margin covers only the moments around a commit, not an entire open transaction.
func handleIndex(ctx context.Context, rt *runtime.Runtime, r *indexRequest) (any, int, error) {
	task := &tasks.IndexKnowledge{KnowledgeUUID: r.KnowledgeUUID}

	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
		return nil, 0, fmt.Errorf("error queueing index knowledge task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
