package contacts_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks/contacts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestImportContactBatch(t *testing.T) {
	_, rt := testsuite.Runtime()
	rc := rt.VK.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetData)

	importID := testdb.InsertContactImport(rt, testdb.Org1, testdb.Admin)
	batch1ID := testdb.InsertContactImportBatch(rt, importID, []byte(`[
		{"name": "Norbert", "language": "eng", "urns": ["tel:+16055740001"]},
		{"name": "Leah", "urns": ["tel:+16055740002"]}
	]`))
	batch2ID := testdb.InsertContactImportBatch(rt, importID, []byte(`[
		{"name": "Rowan", "language": "spa", "urns": ["tel:+16055740003"]}
	]`))

	rc.Do("setex", fmt.Sprintf("contact_import_batches_remaining:%d", importID), 10, 2)

	// perform first batch task...
	testsuite.QueueBatchTask(t, rt, testdb.Org1, &contacts.ImportContactBatchTask{ContactImportBatchID: batch1ID})
	testsuite.FlushTasks(t, rt)

	// import is still in progress
	assertdb.Query(t, rt.DB, `SELECT status FROM contacts_contactimport WHERE id = $1`, importID).Columns(map[string]any{"status": "O"})

	// perform second batch task...
	testsuite.QueueBatchTask(t, rt, testdb.Org1, &contacts.ImportContactBatchTask{ContactImportBatchID: batch2ID})
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
