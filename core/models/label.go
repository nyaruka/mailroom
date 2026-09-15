package models

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core/events"
)

type LabelID int

// Label is our mailroom type for message labels
type Label struct {
	ID_   LabelID          `json:"id"`
	UUID_ assets.LabelUUID `json:"uuid"`
	Name_ string           `json:"name"`
}

// ID returns the ID for this label
func (l *Label) ID() LabelID { return l.ID_ }

// UUID returns the uuid for this label
func (l *Label) UUID() assets.LabelUUID { return l.UUID_ }

// Name returns the name for this label
func (l *Label) Name() string { return l.Name_ }

// loads the labels for the passed in org
func loadLabels(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.Label, error) {
	rows, err := db.QueryContext(ctx, sqlSelectLabelsByOrg, orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying labels for org: %d: %w", orgID, err)
	}

	return ScanJSONRows(rows, func() assets.Label { return &Label{} })
}

const sqlSelectLabelsByOrg = `
SELECT ROW_TO_JSON(r) FROM (
      SELECT id, uuid, name
        FROM msgs_label
       WHERE org_id = $1 AND is_active = TRUE
    ORDER BY name ASC
) r;`

// AddMsgLabels adds the given labels to messages, returning the IDs of the messages whose labels actually changed
func AddMsgLabels(ctx context.Context, tx DBorTx, adds []*MsgLabelAdd) ([]MsgID, error) {
	if len(adds) == 0 {
		return nil, nil
	}

	sql, args, err := dbutil.BulkSQL(tx, sqlInsertMsgLabels, adds)
	if err != nil {
		return nil, fmt.Errorf("error preparing bulk insert of msg labels: %w", err)
	}

	var changed []MsgID
	if err := tx.SelectContext(ctx, &changed, sql, args...); err != nil {
		return nil, fmt.Errorf("error inserting new msg labels: %w", err)
	}
	return changed, nil
}

const sqlInsertMsgLabels = `
INSERT INTO msgs_msg_labels(msg_id, label_id) 
SELECT msgs_msg.id, r.label_id
FROM (VALUES(:msg_uuid::uuid, :label_id::int)) AS r(msg_uuid, label_id)
INNER JOIN msgs_msg ON msgs_msg.uuid = r.msg_uuid
ON CONFLICT DO NOTHING
RETURNING msg_id`

// MsgLabelAdd represents a single label that should be added to a message
type MsgLabelAdd struct {
	MsgUUID events.EventUUID `db:"msg_uuid"`
	LabelID LabelID          `db:"label_id"`
}

// RemoveMsgLabels removes the given label from the given messages, returning the IDs of the messages whose labels
// actually changed
func RemoveMsgLabels(ctx context.Context, tx DBorTx, labelID LabelID, msgIDs []MsgID) ([]MsgID, error) {
	if len(msgIDs) == 0 {
		return nil, nil
	}

	var changed []MsgID
	if err := tx.SelectContext(ctx, &changed, sqlDeleteMsgLabels, labelID, pq.Array(msgIDs)); err != nil {
		return nil, fmt.Errorf("error deleting msg labels: %w", err)
	}
	return changed, nil
}

const sqlDeleteMsgLabels = `
DELETE FROM msgs_msg_labels WHERE label_id = $1 AND msg_id = ANY($2) RETURNING msg_id`
