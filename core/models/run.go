package models

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

type FlowRunID int64

const NilFlowRunID = FlowRunID(0)

type RunStatus string

const (
	RunStatusActive      RunStatus = "A"
	RunStatusWaiting     RunStatus = "W"
	RunStatusCompleted   RunStatus = "C"
	RunStatusExpired     RunStatus = "X"
	RunStatusInterrupted RunStatus = "I"
	RunStatusFailed      RunStatus = "F"
)

var runStatusMap = map[flows.RunStatus]RunStatus{
	flows.RunStatusActive:    RunStatusActive,
	flows.RunStatusWaiting:   RunStatusWaiting,
	flows.RunStatusCompleted: RunStatusCompleted,
	flows.RunStatusExpired:   RunStatusExpired,
	flows.RunStatusFailed:    RunStatusFailed,
}

// FlowRun is the type for a run of a flow
type FlowRun struct {
	ID              FlowRunID         `db:"id"`
	UUID            flows.RunUUID     `db:"uuid"`
	Status          RunStatus         `db:"status"`
	CreatedOn       time.Time         `db:"created_on"`
	ModifiedOn      time.Time         `db:"modified_on"`
	ExitedOn        *time.Time        `db:"exited_on"`
	Responded       bool              `db:"responded"`
	Results         string            `db:"results"`
	PathNodes       pq.StringArray    `db:"path_nodes"`
	PathTimes       pq.GenericArray   `db:"path_times"`
	CurrentNodeUUID null.String       `db:"current_node_uuid"`
	ContactID       ContactID         `db:"contact_id"`
	FlowID          FlowID            `db:"flow_id"`
	OrgID           OrgID             `db:"org_id"`
	SessionUUID     flows.SessionUUID `db:"session_uuid"`
	StartID         StartID           `db:"start_id"`
}

// NewRun creates a flow run we can save to the database
func NewRun(oa *OrgAssets, fs flows.Session, fr flows.Run) *FlowRun {
	// build our path elements
	pathNodes := make(pq.StringArray, len(fr.Path()))
	pathTimes := make([]time.Time, len(fr.Path()))
	for i, p := range fr.Path() {
		pathNodes[i] = string(p.NodeUUID())
		pathTimes[i] = p.ArrivedOn()
	}

	// it's possible to resume a session with previous runs in now deleted flows - we don't need flow ID to update such runs
	var flowID FlowID
	if fr.Flow() != nil {
		flowID = fr.Flow().Asset().(*Flow).ID()
	}

	r := &FlowRun{
		UUID:        fr.UUID(),
		Status:      runStatusMap[fr.Status()],
		CreatedOn:   fr.CreatedOn(),
		ExitedOn:    fr.ExitedOn(),
		ModifiedOn:  fr.ModifiedOn(),
		ContactID:   ContactID(fr.Contact().ID()),
		FlowID:      flowID,
		OrgID:       oa.OrgID(),
		SessionUUID: fs.UUID(),
		StartID:     NilStartID,
		PathNodes:   pathNodes,
		PathTimes:   pq.GenericArray{A: pathTimes},
		Results:     string(jsonx.MustMarshal(fr.Results())),
		Responded:   fr.HadInput(),
	}

	if len(pathNodes) > 0 && (fr.Status() == flows.RunStatusActive || fr.Status() == flows.RunStatusWaiting) {
		r.CurrentNodeUUID = null.String(pathNodes[len(pathNodes)-1])
	}

	return r
}

const sqlInsertRun = `
INSERT INTO
flows_flowrun(uuid, created_on, modified_on, exited_on, status, responded, results, path_nodes, path_times,
	          current_node_uuid, contact_id, flow_id, org_id, session_uuid, start_id)
	   VALUES(:uuid, :created_on, NOW(), :exited_on, :status, :responded, :results, :path_nodes, :path_times,
	          :current_node_uuid, :contact_id, :flow_id, :org_id, :session_uuid, :start_id)
RETURNING id
`

func InsertRuns(ctx context.Context, tx *sqlx.Tx, runs []*FlowRun) error {
	if err := BulkQuery(ctx, "insert runs", tx, sqlInsertRun, runs); err != nil {
		return fmt.Errorf("error inserting runs: %w", err)
	}
	return nil
}

const sqlUpdateRun = `
UPDATE
	flows_flowrun fr
SET
	status = r.status,
	exited_on = r.exited_on::timestamptz,
	responded = r.responded::bool,
	results = r.results,
	path_nodes = r.path_nodes::uuid[],
	path_times = r.path_times::timestamptz[],
	current_node_uuid = r.current_node_uuid::uuid,
	modified_on = NOW()
FROM (
	VALUES(:uuid, :status, :exited_on, :responded, :results, :path_nodes, :path_times, :current_node_uuid)
) AS
	r(uuid, status, exited_on, responded, results, path_nodes, path_times, current_node_uuid)
WHERE
	fr.uuid = r.uuid::uuid`

func UpdateRuns(ctx context.Context, tx *sqlx.Tx, runs []*FlowRun) error {
	if err := BulkQuery(ctx, "update runs", tx, sqlUpdateRun, runs); err != nil {
		return fmt.Errorf("error updating runs: %w", err)
	}
	return nil
}

// GetContactIDsAtNode returns the ids of contacts currently waiting or active at the given flow node
func GetContactIDsAtNode(ctx context.Context, rt *runtime.Runtime, orgID OrgID, nodeUUID flows.NodeUUID) ([]ContactID, error) {
	rows, err := rt.ReadonlyDB.QueryContext(ctx,
		`SELECT contact_id FROM flows_flowrun WHERE org_id = $1 AND current_node_uuid = $2 AND status IN ('A' , 'W')`, orgID, nodeUUID,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error querying contacts at node: %w", err)
	}
	defer rows.Close()

	contactIDs := make([]ContactID, 0, 10)

	for rows.Next() {
		var id ContactID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("error scanning contact id: %w", err)
		}
		contactIDs = append(contactIDs, id)
	}

	return contactIDs, nil
}

type OngoingRun struct {
	ContactID ContactID       `db:"contact_id"`
	RunUUID   flows.RunUUID   `db:"run_uuid"`
	FlowUUID  assets.FlowUUID `db:"flow_uuid"`
	FlowName  string          `db:"flow_name"`
}

func (i *OngoingRun) Flow() *assets.FlowReference {
	return assets.NewFlowReference(i.FlowUUID, i.FlowName)
}

const sqlSelectOngoingRuns = `
    SELECT r.contact_id, r.uuid AS run_uuid, f.uuid AS flow_uuid, f.name AS flow_name
      FROM flows_flowrun r
INNER JOIN flows_flow f ON f.id = r.flow_id
     WHERE session_uuid = ANY($1) AND status IN ('A', 'W')
	 ORDER BY r.id`

// GetOngoingRuns gets all active or waiting runs for the given contacts
func GetOngoingRuns(ctx context.Context, rt *runtime.Runtime, contactIDs []ContactID) (map[ContactID][]*OngoingRun, error) {
	sessionUUIDs, err := getWaitingSessionsForContacts(ctx, rt.DB, contactIDs)
	if err != nil {
		return nil, err
	}

	var all []*OngoingRun

	if err := rt.DB.SelectContext(ctx, &all, sqlSelectOngoingRuns, pq.Array(sessionUUIDs)); err != nil {
		return nil, fmt.Errorf("error fetching ongoing runs: %w", err)
	}

	byContact := make(map[ContactID][]*OngoingRun, len(contactIDs))
	for _, r := range all {
		byContact[r.ContactID] = append(byContact[r.ContactID], r)
	}

	return byContact, nil
}
