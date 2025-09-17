package testdb

import (
	"encoding/json"
	"testing"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/stretchr/testify/require"
)

// InsertContactImport inserts a contact import
func InsertContactImport(t *testing.T, rt *runtime.Runtime, org *Org, status models.ImportStatus, createdBy *User) models.ContactImportID {
	var importID models.ContactImportID
	err := rt.DB.Get(&importID, `INSERT INTO contacts_contactimport(org_id, file, original_filename, mappings, num_records, group_id, started_on, status, created_on, created_by_id, modified_on, modified_by_id, is_active)
					          VALUES($1, 'contact_imports/1234.xlsx', 'contacts.xlsx', '{}', 30, NULL, $2, $3, $2, $4, $2, $4, TRUE) RETURNING id`, org.ID, dates.Now(), status, createdBy.ID,
	)
	require.NoError(t, err)
	return importID
}

// InsertContactImportBatch inserts a contact import batch
func InsertContactImportBatch(t *testing.T, rt *runtime.Runtime, importID models.ContactImportID, specs json.RawMessage) models.ContactImportBatchID {
	var splitSpecs []json.RawMessage
	require.NoError(t, jsonx.Unmarshal(specs, &splitSpecs))

	var batchID models.ContactImportBatchID
	err := rt.DB.Get(&batchID, `INSERT INTO contacts_contactimportbatch(contact_import_id, status, specs, record_start, record_end, num_created, num_updated, num_errored, errors, finished_on)
					         VALUES($1, 'P', $2, 0, $3, 0, 0, 0, '[]', NULL) RETURNING id`, importID, specs, len(splitSpecs),
	)
	require.NoError(t, err)
	return batchID
}
