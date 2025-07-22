package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/imports"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadContactImport(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa := testdb.Org1.Load(rt)

	importID := testdb.InsertContactImport(rt, testdb.Org1, testdb.Admin)
	batch1ID := testdb.InsertContactImportBatch(rt, importID, []byte(`[
		{"name": "Norbert", "language": "eng", "urns": ["tel:+16055740001"]},
		{"name": "Leah", "urns": ["tel:+16055740002"]}
	]`))
	batch2ID := testdb.InsertContactImportBatch(rt, importID, []byte(`[
		{"name": "Rowan", "language": "spa", "urns": ["tel:+16055740003"]}
	]`))
	testdb.InsertContactImport(rt, testdb.Org1, testdb.Editor)

	imp, err := models.LoadContactImport(ctx, rt.DB, importID)
	require.NoError(t, err)

	assert.Equal(t, testdb.Org1.ID, imp.OrgID)
	assert.Equal(t, testdb.Admin.ID, imp.CreatedByID)
	assert.Equal(t, models.ImportStatusProcessing, imp.Status)
	assert.Nil(t, imp.FinishedOn)
	assert.Equal(t, []models.ContactImportBatchID{batch1ID, batch2ID}, imp.BatchIDs)
	assert.Equal(t, []models.ImportStatus{models.ImportStatusPending}, imp.BatchStatuses)

	batch1, err := models.LoadContactImportBatch(ctx, rt.DB, batch1ID)
	require.NoError(t, err)

	assert.Equal(t, importID, batch1.ImportID)
	assert.Equal(t, models.ImportStatusPending, batch1.Status)
	assert.NotNil(t, batch1.Specs)
	assert.Equal(t, 0, batch1.RecordStart)
	assert.Equal(t, 2, batch1.RecordEnd)

	err = imports.ImportBatch(ctx, rt, oa, batch1, testdb.Admin.ID)
	require.NoError(t, err)

	imp, err = models.LoadContactImport(ctx, rt.DB, importID)
	require.NoError(t, err)

	assert.Equal(t, []models.ContactImportBatchID{batch1ID, batch2ID}, imp.BatchIDs)
	assert.ElementsMatch(t, []models.ImportStatus{models.ImportStatusComplete, models.ImportStatusPending}, imp.BatchStatuses)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactimportbatch WHERE status = 'C' AND finished_on IS NOT NULL`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactimportbatch WHERE status = 'P' AND finished_on IS NULL`).Returns(1)
}
