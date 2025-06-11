package models

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nyaruka/goflow/assets"
)

// FieldID is our type for the database field ID
type FieldID int

// Field is our mailroom type for contact field types
type Field struct {
	ID_    FieldID          `json:"id"`
	UUID_  assets.FieldUUID `json:"uuid"`
	Key_   string           `json:"key"`
	Name_  string           `json:"name"`
	Type_  assets.FieldType `json:"field_type"`
	Proxy_ bool             `json:"is_proxy"`
}

// ID returns the ID of this field
func (f *Field) ID() FieldID { return f.ID_ }

// UUID returns the UUID of this field
func (f *Field) UUID() assets.FieldUUID { return f.UUID_ }

// Key returns the key for this field
func (f *Field) Key() string { return f.Key_ }

// Name returns the name for this field
func (f *Field) Name() string { return f.Name_ }

// Type returns the value type for this field
func (f *Field) Type() assets.FieldType { return f.Type_ }

// Proxy returns whether this is a proxy field, e.g. created_on
func (f *Field) Proxy() bool { return f.Proxy_ }

// loadFields loads the assets for the passed in db
func loadFields(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.Field, error) {
	rows, err := db.QueryContext(ctx, sqlSelectFieldsByOrg, orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying fields for org: %d: %w", orgID, err)
	}

	return ScanJSONRows(rows, func() assets.Field { return &Field{} })
}

const sqlSelectFieldsByOrg = `
SELECT ROW_TO_JSON(f) FROM (
      SELECT id, uuid, key, name, (CASE value_type WHEN 'T' THEN 'text' WHEN 'N' THEN 'number' WHEN 'D' THEN 'datetime' WHEN 'S' THEN 'state' WHEN 'I' THEN 'district' WHEN 'W' THEN 'ward' END) AS field_type, is_proxy
        FROM contacts_contactfield 
       WHERE org_id = $1 AND is_active = TRUE
    ORDER BY key ASC
) f;`
