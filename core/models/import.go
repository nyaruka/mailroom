package models

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/null/v3"
)

// ContactImportID is the type for contact import IDs
type ContactImportID int

func (i *ContactImportID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i ContactImportID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *ContactImportID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i ContactImportID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

// ContactImportBatchID is the type for contact import batch IDs
type ContactImportBatchID int

// ContactImportStatus is the status of an import
type ContactImportStatus string

// import status constants
const (
	ContactImportStatusPending    ContactImportStatus = "P"
	ContactImportStatusProcessing ContactImportStatus = "O"
	ContactImportStatusComplete   ContactImportStatus = "C"
	ContactImportStatusFailed     ContactImportStatus = "F"
)

type ContactImport struct {
	ID          ContactImportID     `db:"id"`
	OrgID       OrgID               `db:"org_id"`
	Status      ContactImportStatus `db:"status"`
	CreatedByID UserID              `db:"created_by_id"`
	FinishedOn  *time.Time          `db:"finished_on"`

	// we fetch unique batch statuses concatenated as a string, see https://github.com/jmoiron/sqlx/issues/168
	BatchStatuses string `db:"batch_statuses"`
}

var sqlLoadContactImport = `
         SELECT i.id, i.org_id, i.status, i.created_by_id, i.finished_on, array_to_string(array_agg(DISTINCT b.status), '') AS "batch_statuses"
           FROM contacts_contactimport i
LEFT OUTER JOIN contacts_contactimportbatch b ON b.contact_import_id = i.id
          WHERE i.id = $1
       GROUP BY i.id`

// LoadContactImport loads a contact import by ID
func LoadContactImport(ctx context.Context, db DBorTx, id ContactImportID) (*ContactImport, error) {
	i := &ContactImport{}
	err := db.GetContext(ctx, i, sqlLoadContactImport, id)
	if err != nil {
		return nil, fmt.Errorf("error loading contact import id=%d: %w", id, err)
	}
	return i, nil
}

var sqlMarkContactImportFinished = `
UPDATE contacts_contactimport
   SET status = $2, finished_on = $3
 WHERE id = $1`

func (i *ContactImport) SetFinished(ctx context.Context, db DBorTx, status ContactImportStatus) error {
	now := dates.Now()
	i.Status = status
	i.FinishedOn = &now

	_, err := db.ExecContext(ctx, sqlMarkContactImportFinished, i.ID, i.Status, i.FinishedOn)
	if err != nil {
		return fmt.Errorf("error marking import as finished: %w", err)
	}
	return nil
}

// ContactImportBatch is a batch of contacts within a larger import
type ContactImportBatch struct {
	ID       ContactImportBatchID `db:"id"`
	ImportID ContactImportID      `db:"contact_import_id"`
	Status   ContactImportStatus  `db:"status"`
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
	b.Status = ContactImportStatusProcessing
	_, err := db.ExecContext(ctx, `UPDATE contacts_contactimportbatch SET status = $2 WHERE id = $1`, b.ID, b.Status)
	return err
}

func (b *ContactImportBatch) SetComplete(ctx context.Context, db DBorTx, numCreated, numUpdated, numErrored int, errs []ImportError) error {
	now := dates.Now()

	b.Status = ContactImportStatusComplete
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
	b.Status = ContactImportStatusFailed
	b.FinishedOn = &now
	_, err := db.ExecContext(ctx, `UPDATE contacts_contactimportbatch SET status = $2, finished_on = $3 WHERE id = $1`, b.ID, b.Status, b.FinishedOn)
	return err
}

var loadContactImportBatchSQL = `
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
	err := db.GetContext(ctx, b, loadContactImportBatchSQL, id)
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
