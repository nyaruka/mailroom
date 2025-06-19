package models

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
)

// GroupID is our type for group ids
type GroupID int

// GroupStatus is the current status of the passed in group
type GroupStatus string

const (
	GroupStatusInitializing = GroupStatus("I")
	GroupStatusEvaluating   = GroupStatus("V")
	GroupStatusReady        = GroupStatus("R")
)

// GroupType is the the type of a group
type GroupType string

const (
	GroupTypeDBActive   = GroupType("A")
	GroupTypeDBBlocked  = GroupType("B")
	GroupTypeDBStopped  = GroupType("S")
	GroupTypeDBArchived = GroupType("V")
	GroupTypeManual     = GroupType("M")
	GroupTypeSmart      = GroupType("Q")
)

// Group is our mailroom type for contact groups
type Group struct {
	ID_     GroupID          `json:"id"`
	UUID_   assets.GroupUUID `json:"uuid"`
	Name_   string           `json:"name"`
	Query_  string           `json:"query"`
	Status_ GroupStatus      `json:"status"`
	Type_   GroupType        `json:"group_type"`
}

// ID returns the ID for this group
func (g *Group) ID() GroupID { return g.ID_ }

// UUID returns the uuid for this group
func (g *Group) UUID() assets.GroupUUID { return g.UUID_ }

// Name returns the name for this group
func (g *Group) Name() string { return g.Name_ }

// Query returns the query string (if any) for this group
func (g *Group) Query() string { return g.Query_ }

// Status returns the status of this group
func (g *Group) Status() GroupStatus { return g.Status_ }

// Type returns the type of this group
func (g *Group) Type() GroupType { return g.Type_ }

// Visible returns whether this group is visible to the engine (status groups are not)
func (g *Group) Visible() bool { return g.Type_ == GroupTypeManual || g.Type_ == GroupTypeSmart }

// loads the groups for the passed in org
func loadGroups(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.Group, error) {
	rows, err := db.QueryContext(ctx, sqlSelectGroupsByOrg, orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying groups for org: %d: %w", orgID, err)
	}

	return ScanJSONRows(rows, func() assets.Group { return &Group{} })
}

const sqlSelectGroupsByOrg = `
SELECT ROW_TO_JSON(r) FROM (
      SELECT id, uuid, name, query, status, group_type
        FROM contacts_contactgroup 
       WHERE org_id = $1 AND is_active = TRUE
    ORDER BY name ASC
) r;`

// RemoveContactsFromGroups fires a bulk SQL query to remove all the contacts in the passed in groups
func RemoveContactsFromGroups(ctx context.Context, tx DBorTx, removals []*GroupRemove) error {
	return BulkQuery(ctx, "removing contacts from groups", tx, removeContactsFromGroupsSQL, removals)
}

// GroupRemove is our struct to track group removals
type GroupRemove struct {
	ContactID ContactID `db:"contact_id"`
	GroupID   GroupID   `db:"group_id"`
}

const removeContactsFromGroupsSQL = `
DELETE FROM
	contacts_contactgroup_contacts
WHERE 
	id
IN (
	SELECT 
		c.id 
	FROM 
		contacts_contactgroup_contacts c,
		(VALUES(:contact_id, :group_id)) AS g(contact_id, group_id)
	WHERE
		c.contact_id = g.contact_id::int AND c.contactgroup_id = g.group_id::int
);
`

// AddContactsToGroups fires a bulk SQL query to remove all the contacts in the passed in groups
func AddContactsToGroups(ctx context.Context, tx DBorTx, adds []*GroupAdd) error {
	return BulkQuery(ctx, "adding contacts to groups", tx, sqlAddContactsToGroups, adds)
}

// GroupAdd is our struct to track a final group additions
type GroupAdd struct {
	ContactID ContactID `db:"contact_id"`
	GroupID   GroupID   `db:"group_id"`
}

const sqlAddContactsToGroups = `
INSERT INTO contacts_contactgroup_contacts(contact_id, contactgroup_id)
                                    VALUES(:contact_id, :group_id)
ON CONFLICT DO NOTHING`

// GetGroupContactCount returns the total number of contacts that are in given group
func GetGroupContactCount(ctx context.Context, db *sql.DB, groupID GroupID) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT SUM(count) FROM contacts_contactgroupcount WHERE group_id = $1 GROUP BY group_id`, groupID).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("error getting group contact count: %w", err)
	}
	return count, nil
}

// GetGroupContactIDs returns the ids of the contacts that are in given group
func GetGroupContactIDs(ctx context.Context, tx DBorTx, groupID GroupID) ([]ContactID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT contact_id FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1`, groupID)
	if err != nil {
		return nil, fmt.Errorf("error selecting contact ids for group: %w", err)
	}

	contactIDs := make([]ContactID, 0, 10)

	contactIDs, err = dbutil.ScanAllSlice(rows, contactIDs)
	if err != nil {
		return nil, fmt.Errorf("error scanning contact ids: %w", err)
	}
	return contactIDs, nil
}

const updateGroupStatusSQL = `UPDATE contacts_contactgroup SET status = $2 WHERE id = $1`

// UpdateGroupStatus updates the group status for the passed in group
func UpdateGroupStatus(ctx context.Context, db DBorTx, groupID GroupID, status GroupStatus) error {
	_, err := db.ExecContext(ctx, updateGroupStatusSQL, groupID, status)
	if err != nil {
		return fmt.Errorf("error updating group status for group: %d: %w", groupID, err)
	}
	return nil
}

// RemoveContactsFromGroupAndCampaigns removes the passed in contacts from the passed in group, taking care of also
// removing them from any associated campaigns
func RemoveContactsFromGroupAndCampaigns(ctx context.Context, db *sqlx.DB, oa *OrgAssets, groupID GroupID, contactIDs []ContactID) error {
	removeBatch := func(batch []ContactID) error {
		tx, err := db.BeginTxx(ctx, nil)

		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error starting transaction: %w", err)
		}

		removals := make([]*GroupRemove, len(batch))
		for i, cid := range batch {
			removals[i] = &GroupRemove{
				GroupID:   groupID,
				ContactID: cid,
			}
		}
		err = RemoveContactsFromGroups(ctx, tx, removals)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error removing contacts from group: %d: %w", groupID, err)
		}

		err = DeleteCampaignFiresForGroupRemoval(ctx, tx, oa, batch, groupID)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error deleting campaign fires for group: %d: %w", groupID, err)
		}

		err = tx.Commit()
		if err != nil {
			return fmt.Errorf("error commiting batch removal of contacts for group: %d: %w", groupID, err)
		}

		return nil
	}

	// batch up our contacts for removal, 500 at a time
	batch := make([]ContactID, 0, 100)
	for _, id := range contactIDs {
		batch = append(batch, id)

		if len(batch) == 500 {
			err := removeBatch(batch)
			if err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		err := removeBatch(batch)
		if err != nil {
			return err
		}
	}

	return nil
}

// AddContactsToGroupAndCampaigns takes care of adding the passed in contacts to the passed in group, updating any
// associated campaigns as needed
func AddContactsToGroupAndCampaigns(ctx context.Context, db *sqlx.DB, oa *OrgAssets, groupID GroupID, contactIDs []ContactID) error {
	// we need session assets in order to recalculate campaign fires
	addBatch := func(batch []ContactID) error {
		tx, err := db.BeginTxx(ctx, nil)

		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error starting transaction: %w", err)
		}

		adds := make([]*GroupAdd, len(batch))
		for i, cid := range batch {
			adds[i] = &GroupAdd{
				GroupID:   groupID,
				ContactID: cid,
			}
		}
		err = AddContactsToGroups(ctx, tx, adds)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error adding contacts to group: %d: %w", groupID, err)
		}

		// now load our contacts
		contacts, err := LoadContacts(ctx, tx, oa, batch)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error loading contacts when adding to group: %d: %w", groupID, err)
		}

		// convert to flow contacts
		fcs := make([]*flows.Contact, len(contacts))
		for i, c := range contacts {
			fcs[i], err = c.EngineContact(oa)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("error converting contact to flow contact: %s: %w", c.UUID(), err)
			}
		}

		// schedule any upcoming events that were affected by this group
		err = AddCampaignFiresForGroupAddition(ctx, tx, oa, fcs, groupID)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error calculating new campaign fires during group addition: %d: %w", groupID, err)
		}

		err = tx.Commit()
		if err != nil {
			return fmt.Errorf("error commiting batch addition of contacts for group: %d: %w", groupID, err)
		}

		return nil
	}

	// add our contacts in batches of 500
	batch := make([]ContactID, 0, 500)
	for _, id := range contactIDs {
		batch = append(batch, id)

		if len(batch) == 500 {
			err := addBatch(batch)
			if err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		err := addBatch(batch)
		if err != nil {
			return err
		}
	}

	return nil
}
