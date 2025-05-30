package models_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFields(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFields)
	require.NoError(t, err)

	fields, err := oa.Fields()
	require.NoError(t, err)
	assert.Len(t, fields, 6) // excludes the proxy fields
	assert.Equal(t, "age", fields[0].Key())
	assert.Equal(t, "Age", fields[0].Name())
	assert.Equal(t, assets.FieldTypeNumber, fields[0].Type())

	expectedFields := []struct {
		field     testdb.Field
		key       string
		name      string
		valueType assets.FieldType
	}{
		{*testdb.GenderField, "gender", "Gender", assets.FieldTypeText},
		{*testdb.AgeField, "age", "Age", assets.FieldTypeNumber},
		{*testdb.CreatedOnField, "created_on", "Created On", assets.FieldTypeDatetime},
		{*testdb.LastSeenOnField, "last_seen_on", "Last Seen On", assets.FieldTypeDatetime},
	}
	for _, tc := range expectedFields {
		field := oa.FieldByUUID(tc.field.UUID)
		require.NotNil(t, field, "no such field: %s", tc.field.UUID)

		fieldByKey := oa.FieldByKey(tc.key)
		assert.Equal(t, field, fieldByKey)

		assert.Equal(t, tc.field.UUID, field.UUID(), "uuid mismatch for field %s", tc.field.ID)
		assert.Equal(t, tc.key, field.Key())
		assert.Equal(t, tc.name, field.Name())
		assert.Equal(t, tc.valueType, field.Type())
	}
}
