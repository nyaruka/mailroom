package contact

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/import", web.JSONPayload(handleImport))
}

// Request that a contact import is started.
//
//	{
//	  "org_id": 1,
//	  "import_id": 123
//	}
type importRequest struct {
	OrgID    models.OrgID           `json:"org_id"    validate:"required"`
	ImportID models.ContactImportID `json:"import_id" validate:"required"`
}

func handleImport(ctx context.Context, rt *runtime.Runtime, r *importRequest) (any, int, error) {
	imp, err := models.LoadContactImport(ctx, rt.DB, r.ImportID)
	if err != nil {
		return nil, 0, err
	}
	if imp.OrgID != r.OrgID {
		panic("request org id does not match import org id")
	}
	if imp.Status != models.ImportStatusProcessing {
		return nil, 0, fmt.Errorf("import is not processing")
	}

	// set valkey tracker which batch tasks can mark themselves complete in to know when import has completed
	batchIDs := make([]string, len(imp.BatchIDs))
	for i, bID := range imp.BatchIDs {
		batchIDs[i] = strconv.Itoa(int(bID))
	}
	if err := tasks.ContactImportTracker(imp.ID).Init(ctx, rt.VK, batchIDs); err != nil {
		return nil, 0, fmt.Errorf("error setting import batch tracker key: %w", err)
	}

	// create tasks for all batches
	for _, bID := range imp.BatchIDs {
		task := &tasks.ImportContactBatch{ContactImportBatchID: bID}
		if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, false); err != nil {
			return nil, 0, fmt.Errorf("error queuing import contact batch task: %w", err)
		}
	}

	return map[string]any{"batches": len(imp.BatchIDs)}, http.StatusOK, nil
}
