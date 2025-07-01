package contacts

import (
	"context"
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/mailroom/core/imports"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeImportContactBatch is the type of the import contact batch task
const TypeImportContactBatch = "import_contact_batch"

func init() {
	tasks.RegisterType(TypeImportContactBatch, func() tasks.Task { return &ImportContactBatchTask{} })
}

// ImportContactBatchTask is our task to import a batch of contacts
type ImportContactBatchTask struct {
	ContactImportBatchID models.ContactImportBatchID `json:"contact_import_batch_id"`
}

func (t *ImportContactBatchTask) Type() string {
	return TypeImportContactBatch
}

// Timeout is the maximum amount of time the task can run for
func (t *ImportContactBatchTask) Timeout() time.Duration {
	return time.Minute * 10
}

func (t *ImportContactBatchTask) WithAssets() models.Refresh {
	return models.RefreshFields | models.RefreshGroups
}

// Perform figures out the membership for a query based group then repopulates it
func (t *ImportContactBatchTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	batch, err := models.LoadContactImportBatch(ctx, rt.DB, t.ContactImportBatchID)
	if err != nil {
		return fmt.Errorf("error loading contact import batch: %w", err)
	}

	imp, err := models.LoadContactImport(ctx, rt.DB, batch.ImportID)
	if err != nil {
		return fmt.Errorf("error loading contact import: %w", err)
	}

	batchErr := imports.ImportBatch(ctx, rt, oa, batch, imp.CreatedByID)

	// if any error occurs this batch should be marked as failed
	if batchErr != nil {
		batch.SetFailed(ctx, rt.DB)
	}

	// decrement the key that holds remaining batches to see if the overall import is now finished
	rc := rt.VK.Get()
	defer rc.Close()
	remaining, _ := redis.Int(rc.Do("decr", fmt.Sprintf("contact_import_batches_remaining:%d", batch.ImportID)))
	if remaining == 0 {
		// if any batch failed, then import is considered failed
		status := models.ContactImportStatusComplete
		for _, s := range imp.BatchStatuses {
			if models.ContactImportStatus(s) == models.ContactImportStatusFailed {
				status = models.ContactImportStatusFailed
				break
			}
		}

		if err := imp.SetFinished(ctx, rt.DB, status); err != nil {
			return fmt.Errorf("error marking import as finished: %w", err)
		}

		if err := models.NotifyImportFinished(ctx, rt.DB, imp); err != nil {
			return fmt.Errorf("error creating import finished notification: %w", err)
		}
	}

	if batchErr != nil {
		return fmt.Errorf("unable to import contact import batch %d: %w", t.ContactImportBatchID, batchErr)
	}

	return nil
}
