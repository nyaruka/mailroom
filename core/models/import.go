package models

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/null/v3"
)

// ImportStatus is the status of an import
type ImportStatus string

// import status constants
const (
	ImportStatusPending    ImportStatus = "P"
	ImportStatusProcessing ImportStatus = "O"
	ImportStatusComplete   ImportStatus = "C"
	ImportStatusFailed     ImportStatus = "F"
)

// ContactImportID is the type for contact import IDs
type ContactImportID int

func (i *ContactImportID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i ContactImportID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *ContactImportID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i ContactImportID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

// ContactImportBatchID is the type for contact import batch IDs
type ContactImportBatchID int

func (i *ContactImportBatchID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i ContactImportBatchID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *ContactImportBatchID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i ContactImportBatchID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

type ContactImport struct {
	ID          ContactImportID `json:"id"`
	OrgID       OrgID           `json:"org_id"`
	Status      ImportStatus    `json:"status"`
	CreatedByID UserID          `json:"created_by_id"`
	FinishedOn  *time.Time      `json:"finished_on"`

	BatchIDs      []ContactImportBatchID `json:"batch_ids"`      // ordered
	BatchStatuses []ImportStatus         `json:"batch_statuses"` // unique values only
}

var sqlLoadContactImport = `
SELECT row_to_json(r) FROM (
         SELECT i.id, i.org_id, i.status, i.created_by_id, i.finished_on, array_agg(b.id ORDER BY b.id) AS "batch_ids", array_agg(DISTINCT b.status) AS "batch_statuses"
           FROM contacts_contactimport i
LEFT OUTER JOIN contacts_contactimportbatch b ON b.contact_import_id = i.id
          WHERE i.id = $1
       GROUP BY i.id
) r`

// LoadContactImport loads a contact import by ID
func LoadContactImport(ctx context.Context, db *sqlx.DB, id ContactImportID) (*ContactImport, error) {
	row := db.QueryRowContext(ctx, sqlLoadContactImport, id)

	i := &ContactImport{}
	if err := dbutil.ScanJSON(row, i); err != nil {
		return nil, fmt.Errorf("error fetching contact import by id %d: %w", id, err)
	}

	return i, nil
}

func (i *ContactImport) SetFinished(ctx context.Context, db DBorTx, success bool) error {
	i.Status = ImportStatusComplete
	if !success {
		i.Status = ImportStatusFailed
	}

	now := dates.Now()
	i.FinishedOn = &now

	_, err := db.ExecContext(ctx, `UPDATE contacts_contactimport SET status = $2, finished_on = $3 WHERE id = $1`, i.ID, i.Status, i.FinishedOn)
	if err != nil {
		return fmt.Errorf("error marking import as finished: %w", err)
	}
	return nil
}

// ContactImportBatch is a batch of contacts within a larger import
type ContactImportBatch struct {
	ID       ContactImportBatchID `db:"id"`
	ImportID ContactImportID      `db:"contact_import_id"`
	Status   ImportStatus         `db:"status"`
	Specs    json.RawMessage      `db:"specs"`

	// the range of records from the entire import contained in this batch
	RecordStart int `db:"record_start"`
	RecordEnd   int `db:"record_end"`

	// results written after processing this batch
	NumCreated int             `db:"num_created"`
	NumUpdated int             `db:"num_updated"`
	NumErrored int             `db:"num_errored"`
	Errors     json.RawMessage `db:"errors"`
	FinishedOn *time.Time      `db:"finished_on"`
}

func (b *ContactImportBatch) SetProcessing(ctx context.Context, db DBorTx) error {
	b.Status = ImportStatusProcessing

	_, err := db.ExecContext(ctx, `UPDATE contacts_contactimportbatch SET status = $2 WHERE id = $1`, b.ID, b.Status)
	if err != nil {
		return fmt.Errorf("error marking import as processing: %w", err)
	}

	return err
}

func (b *ContactImportBatch) SetComplete(ctx context.Context, db DBorTx, numCreated, numUpdated, numErrored int, errs []ImportError) error {
	now := dates.Now()

	b.Status = ImportStatusComplete
	b.NumCreated = numCreated
	b.NumUpdated = numUpdated
	b.NumErrored = numErrored
	b.Errors = jsonx.MustMarshal(errs)
	b.FinishedOn = &now

	_, err := db.NamedExecContext(ctx,
		`UPDATE 
			contacts_contactimportbatch
		SET 
			status = :status, 
			num_created = :num_created, 
			num_updated = :num_updated, 
			num_errored = :num_errored, 
			errors = :errors, 
			finished_on = :finished_on 
		WHERE 
			id = :id`,
		b,
	)
	return err
}

func (b *ContactImportBatch) SetFailed(ctx context.Context, db DBorTx) error {
	now := dates.Now()
	b.Status = ImportStatusFailed
	b.FinishedOn = &now
	_, err := db.ExecContext(ctx, `UPDATE contacts_contactimportbatch SET status = $2, finished_on = $3 WHERE id = $1`, b.ID, b.Status, b.FinishedOn)
	return err
}

var sqlLoadContactImportBatch = `
SELECT 
	id,
  	contact_import_id,
  	status,
  	specs,
  	record_start,
  	record_end
FROM
	contacts_contactimportbatch
WHERE
	id = $1`

// LoadContactImportBatch loads a contact import batch by ID
func LoadContactImportBatch(ctx context.Context, db DBorTx, id ContactImportBatchID) (*ContactImportBatch, error) {
	b := &ContactImportBatch{}
	err := db.GetContext(ctx, b, sqlLoadContactImportBatch, id)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ContactSpec describes a contact to be updated or created
type ContactSpec struct {
	UUID     flows.ContactUUID   `json:"uuid"`
	Name     *string             `json:"name"`
	Language *string             `json:"language"`
	Status   flows.ContactStatus `json:"status"`
	URNs     []urns.URN          `json:"urns"`
	Fields   map[string]string   `json:"fields"`
	Groups   []assets.GroupUUID  `json:"groups"`

	ImportRow int `json:"_import_row"`
}

// an error message associated with a particular record
type ImportError struct {
	Record  int    `json:"record"`
	Row     int    `json:"row"`
	Message string `json:"message"`
}
