package models

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

type ContactFireID int64
type ContactFireType string

const (
	ContactFireTypeWaitTimeout       ContactFireType = "T"
	ContactFireTypeWaitExpiration    ContactFireType = "E"
	ContactFireTypeSessionExpiration ContactFireType = "S"
	ContactFireTypeCampaignPoint     ContactFireType = "C"
)

type ContactFire struct {
	ID          ContactFireID   `db:"id"`
	OrgID       OrgID           `db:"org_id"`
	ContactID   ContactID       `db:"contact_id"`
	Type        ContactFireType `db:"fire_type"`
	Scope       string          `db:"scope"`
	FireOn      time.Time       `db:"fire_on"`
	SessionUUID null.String     `db:"session_uuid"`
	SprintUUID  null.String     `db:"sprint_uuid"` // set for wait expirations and timeouts
}

func newContactFire(orgID OrgID, contactID ContactID, typ ContactFireType, scope string, fireOn time.Time, sessionUUID flows.SessionUUID, sprintUUID flows.SprintUUID) *ContactFire {
	return &ContactFire{
		OrgID:       orgID,
		ContactID:   contactID,
		Type:        typ,
		Scope:       scope,
		FireOn:      fireOn,
		SessionUUID: null.String(sessionUUID),
		SprintUUID:  null.String(sprintUUID),
	}
}

func NewFireForSession(orgID OrgID, contactID ContactID, sessionUUID flows.SessionUUID, sprintUUID flows.SprintUUID, typ ContactFireType, fireOn time.Time) *ContactFire {
	return newContactFire(orgID, contactID, typ, "", fireOn, sessionUUID, sprintUUID)
}

func NewContactFireForCampaign(orgID OrgID, contactID ContactID, ce *CampaignPoint, fireOn time.Time) *ContactFire {
	return newContactFire(orgID, contactID, ContactFireTypeCampaignPoint, fmt.Sprintf("%d:%d", ce.ID, ce.FireVersion), fireOn, "", "")
}

const sqlSelectDueContactFires = `
  SELECT id, org_id, contact_id, fire_type, scope, session_uuid, sprint_uuid, fire_on
    FROM contacts_contactfire
   WHERE fire_on < NOW()
ORDER BY fire_on ASC
   LIMIT $1`

// LoadDueContactfires returns up to 10,000 contact fires that are due to be fired.
func LoadDueContactfires(ctx context.Context, rt *runtime.Runtime, limit int) ([]*ContactFire, error) {
	rows, err := rt.DB.QueryxContext(ctx, sqlSelectDueContactFires, limit)
	if err != nil {
		return nil, fmt.Errorf("error querying due contact fires: %w", err)
	}
	defer rows.Close()

	fires := make([]*ContactFire, 0, 50)

	for rows.Next() {
		f := &ContactFire{}
		if err := rows.StructScan(f); err != nil {
			return nil, fmt.Errorf("error scanning contact fire: %w", err)
		}
		fires = append(fires, f)
	}

	return fires, nil
}

// DeleteContactFires deletes the given contact fires
func DeleteContactFires(ctx context.Context, rt *runtime.Runtime, fires []*ContactFire) error {
	ids := make([]ContactFireID, len(fires))
	for i, f := range fires {
		ids[i] = f.ID
	}

	_, err := rt.DB.ExecContext(ctx, `DELETE FROM contacts_contactfire WHERE id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("error deleting contact fires: %w", err)
	}

	return nil
}

// DeleteSessionFires deletes session wait/timeout fires for the given contacts
func DeleteSessionFires(ctx context.Context, db DBorTx, contactIDs []ContactID, incSessionExpiration bool) (int, error) {
	types := []ContactFireType{ContactFireTypeWaitTimeout, ContactFireTypeWaitExpiration}
	if incSessionExpiration {
		types = append(types, ContactFireTypeSessionExpiration)
	}

	res, err := db.ExecContext(ctx, `DELETE FROM contacts_contactfire WHERE contact_id = ANY($1) AND fire_type = ANY($2) AND scope = ''`, pq.Array(contactIDs), pq.Array(types))
	if err != nil {
		return 0, fmt.Errorf("error deleting session contact fires: %w", err)
	}

	numDeleted, _ := res.RowsAffected()
	return int(numDeleted), nil
}

// DeleteAllCampaignFires deletes *all* campaign fires for the given contacts
func DeleteAllCampaignFires(ctx context.Context, db DBorTx, contactIDs []ContactID) error {
	_, err := db.ExecContext(ctx, `DELETE FROM contacts_contactfire WHERE contact_id = ANY($1) AND fire_type = 'C'`, pq.Array(contactIDs))
	if err != nil {
		return fmt.Errorf("error deleting campaign fires: %w", err)
	}

	return nil
}

// FireDelete is a helper struct for deleting specific campaign fires
type FireDelete struct {
	ContactID   ContactID `db:"contact_id"`
	EventID     PointID   `db:"event_id"`
	FireVersion int       `db:"fire_version"`
}

// note that : is escaped as \x3A to stop sqlx mistakenly treating it as a named variable
const sqlDeleteCampaignContactFires = `
DELETE FROM contacts_contactfire WHERE id IN (
    SELECT cf.id FROM contacts_contactfire cf, (VALUES(:contact_id::int, :event_id::int, :fire_version)) AS f(contact_id, event_id, fire_version)
     WHERE cf.contact_id = f.contact_id AND fire_type = 'C' AND cf.scope = f.event_id || E'\x3A' || f.fire_version
)`

// DeleteCampaignFires deletes *specific* campaign fires for the given contacts
func DeleteCampaignFires(ctx context.Context, db DBorTx, deletes []*FireDelete) error {
	return BulkQueryBatches(ctx, "deleting campaign fires", db, sqlDeleteCampaignContactFires, 1000, deletes)
}

var sqlInsertContactFires = `
INSERT INTO contacts_contactfire( org_id,  contact_id,  fire_type,  scope,  fire_on,  session_uuid,  sprint_uuid)
                          VALUES(:org_id, :contact_id, :fire_type, :scope, :fire_on, :session_uuid, :sprint_uuid)
ON CONFLICT DO NOTHING`

// InsertContactFires inserts the given contact fires (no error on conflict)
func InsertContactFires(ctx context.Context, db DBorTx, fs []*ContactFire) error {
	return BulkQueryBatches(ctx, "inserted contact fires", db, sqlInsertContactFires, 1000, fs)
}
