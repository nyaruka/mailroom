package tasks_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	_ "github.com/nyaruka/mailroom/v26/core/runner/handlers"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportContactBatch(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetDynamo|testsuite.ResetValkey)

	importID := testdb.InsertContactImport(t, rt, testdb.Org1, models.ImportStatusProcessing, testdb.Admin)
	batch1ID := testdb.InsertContactImportBatch(t, rt, importID, []byte(`[
		{"name": "Norbert", "language": "eng", "urns": ["tel:+16055740001"]},
		{"name": "Leah", "urns": ["tel:+16055740002"]}
	]`))
	batch2ID := testdb.InsertContactImportBatch(t, rt, importID, []byte(`[
		{"name": "Rowan", "language": "spa", "urns": ["tel:+16055740003"]}
	]`))

	// batches carry an owner UUID and total as the import endpoint creates them
	batchTask := tasks.BatchTask{BatchOwnerUUID: "440e0ac2-8607-42d3-9a30-cb27b29a5c95", TotalBatches: 2}

	// perform first batch task...
	testsuite.QueueBatchTask(t, rt, testdb.Org1, &tasks.ImportContactBatch{BatchTask: batchTask, ContactImportBatchID: batch1ID})
	testsuite.FlushTasks(t, rt)

	// import is still in progress
	assertdb.Query(t, rt.DB, `SELECT status FROM contacts_contactimport WHERE id = $1`, importID).Columns(map[string]any{"status": "O"})

	// perform second batch task...
	testsuite.QueueBatchTask(t, rt, testdb.Org1, &tasks.ImportContactBatch{BatchTask: batchTask, ContactImportBatchID: batch2ID})
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id >= 30000`).Returns(3)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE name = 'Norbert' AND language = 'eng'`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE name = 'Leah' AND language IS NULL`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE name = 'Rowan' AND language = 'spa'`).Returns(1)

	// import is now complete and there is a notification for the creator
	assertdb.Query(t, rt.DB, `SELECT status FROM contacts_contactimport WHERE id = $1`, importID).Columns(map[string]any{"status": "C"})
	assertdb.Query(t, rt.DB, `SELECT org_id, notification_type, scope, user_id FROM notifications_notification WHERE contact_import_id = $1`, importID).
		Columns(map[string]any{
			"org_id":            int64(testdb.Org1.ID),
			"notification_type": "import:finished",
			"scope":             fmt.Sprintf("contact:%d", importID),
			"user_id":           int64(testdb.Admin.ID),
		})
}

func TestImportContactBatchFailure(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetDynamo|testsuite.ResetValkey)

	importID := testdb.InsertContactImport(t, rt, testdb.Org1, models.ImportStatusProcessing, testdb.Admin)

	// insert a batch with specs that can't be unmarshaled so that processing it fails
	batchID := testdb.InsertContactImportBatch(t, rt, importID, []byte(`[{"urns": "should-be-an-array"}]`))

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	task := &tasks.ImportContactBatch{
		BatchTask:            tasks.BatchTask{BatchOwnerUUID: "50aef7cc-8dbd-410b-b6b8-3f2b0bfb0a7b", TotalBatches: 1},
		ContactImportBatchID: batchID,
	}
	assert.Error(t, task.Perform(ctx, rt, oa, testTaskID))

	// batch and overall import should be marked as failed
	assertdb.Query(t, rt.DB, `SELECT status FROM contacts_contactimportbatch WHERE id = $1`, batchID).Columns(map[string]any{"status": "F"})
	assertdb.Query(t, rt.DB, `SELECT status FROM contacts_contactimport WHERE id = $1`, importID).Columns(map[string]any{"status": "F"})

	// and the creator still notified that the import finished
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_notification WHERE contact_import_id = $1 AND notification_type = 'import:finished' AND user_id = $2`, importID, testdb.Admin.ID).Returns(1)
}
