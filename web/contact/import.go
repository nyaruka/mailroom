package contact

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/uuids"
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

	// generate a UUID to own this set of batches since unlike other batch tasks, these have no parent task
	ownerUUID := uuids.NewV7()

	// tracking is best-effort, so an error getting the org name for it shouldn't fail the import
	var orgName string
	if oa, err := models.GetOrgAssets(ctx, rt, r.OrgID); err != nil {
		slog.Error("error loading org assets for batch tracking", "error", err, "org_id", r.OrgID)
	} else {
		orgName = oa.Org().Name()
	}

	tasks.RecordQueued(ctx, rt, ownerUUID, &tasks.BatchInfo{
		Type:     tasks.TypeImportContactBatch,
		OrgID:    r.OrgID,
		OrgName:  orgName,
		Total:    len(imp.BatchIDs),
		QueuedOn: dates.Now(),
	})

	// create tasks for all batches
	for _, bID := range imp.BatchIDs {
		task := &tasks.ImportContactBatch{
			BatchTask:            tasks.BatchTask{BatchOwnerUUID: ownerUUID, TotalBatches: len(imp.BatchIDs)},
			ContactImportBatchID: bID,
		}
		if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, false); err != nil {
			return nil, 0, fmt.Errorf("error queuing import contact batch task: %w", err)
		}
	}

	return map[string]any{"batches": len(imp.BatchIDs)}, http.StatusOK, nil
}
