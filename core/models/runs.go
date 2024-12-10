package models

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
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

// FlowRun is the mailroom type for a FlowRun
type FlowRun struct {
	ID              FlowRunID       `db:"id"`
	UUID            flows.RunUUID   `db:"uuid"`
	Status          RunStatus       `db:"status"`
	CreatedOn       time.Time       `db:"created_on"`
	ModifiedOn      time.Time       `db:"modified_on"`
	ExitedOn        *time.Time      `db:"exited_on"`
	Responded       bool            `db:"responded"`
	Results         string          `db:"results"`
	Path            string          `db:"path"`
	PathNodes       pq.StringArray  `db:"path_nodes"`
	PathTimes       pq.GenericArray `db:"path_times"`
	CurrentNodeUUID null.String     `db:"current_node_uuid"`
	ContactID       ContactID       `db:"contact_id"`
	FlowID          FlowID          `db:"flow_id"`
	OrgID           OrgID           `db:"org_id"`
	SessionID       SessionID       `db:"session_id"`
	StartID         StartID         `db:"start_id"`

	// we keep a reference to the engine's run
	run flows.Run
}

// Step represents a single step in a run, this struct is used for serialization to the steps
type Step struct {
	UUID      flows.StepUUID `json:"uuid"`
	NodeUUID  flows.NodeUUID `json:"node_uuid"`
	ArrivedOn time.Time      `json:"arrived_on"`
	ExitUUID  flows.ExitUUID `json:"exit_uuid,omitempty"`
}

// newRun creates a flow run we can save to the database
func newRun(ctx context.Context, tx *sqlx.Tx, oa *OrgAssets, session *Session, fr flows.Run) (*FlowRun, error) {
	// build our path elements
	path := make([]Step, len(fr.Path()))
	pathNodes := make(pq.StringArray, len(fr.Path()))
	pathTimes := make([]time.Time, len(fr.Path()))
	for i, p := range fr.Path() {
		path[i].UUID = p.UUID()
		path[i].NodeUUID = p.NodeUUID()
		path[i].ArrivedOn = p.ArrivedOn()
		path[i].ExitUUID = p.ExitUUID()

		pathNodes[i] = string(p.NodeUUID())
		pathTimes[i] = p.ArrivedOn()
	}

	// build results dict keyed by snakified name
	results := make(map[string]*flows.Result, len(fr.Results()))
	for resultName, result := range fr.Results() {
		results[utils.Snakify(resultName)] = result
	}

	flowID, err := FlowIDForUUID(ctx, tx, oa, fr.FlowReference().UUID)
	if err != nil {
		return nil, fmt.Errorf("unable to load flow with uuid: %s: %w", fr.FlowReference().UUID, err)
	}

	r := &FlowRun{
		UUID:       fr.UUID(),
		Status:     runStatusMap[fr.Status()],
		CreatedOn:  fr.CreatedOn(),
		ExitedOn:   fr.ExitedOn(),
		ModifiedOn: fr.ModifiedOn(),
		ContactID:  ContactID(fr.Contact().ID()),
		FlowID:     flowID,
		OrgID:      oa.OrgID(),
		SessionID:  session.ID(),
		StartID:    NilStartID,
		Path:       string(jsonx.MustMarshal(path)),
		PathNodes:  pathNodes,
		PathTimes:  pq.GenericArray{A: pathTimes},
		Results:    string(jsonx.MustMarshal(results)),

		run: fr,
	}

	if len(path) > 0 {
		r.CurrentNodeUUID = null.String(path[len(path)-1].NodeUUID)
	}

	// mark ourselves as responded if we received a message
	for _, e := range fr.Events() {
		if e.Type() == events.TypeMsgReceived {
			r.Responded = true
			break
		}
	}

	return r, nil
}

const sqlInsertRun = `
INSERT INTO
flows_flowrun(uuid, created_on, modified_on, exited_on, status, responded, results, path, path_nodes, path_times,
	          current_node_uuid, contact_id, flow_id, org_id, session_id, start_id)
	   VALUES(:uuid, :created_on, NOW(), :exited_on, :status, :responded, :results, :path, :path_nodes, :path_times,
	          :current_node_uuid, :contact_id, :flow_id, :org_id, :session_id, :start_id)
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
	path = r.path::jsonb,
	path_nodes = r.path_nodes::uuid[],
	path_times = r.path_times::timestamptz[],
	current_node_uuid = r.current_node_uuid::uuid,
	modified_on = NOW()
FROM (
	VALUES(:uuid, :status, :exited_on, :responded, :results, :path, :path_nodes, :path_times, :current_node_uuid)
) AS
	r(uuid, status, exited_on, responded, results, path, path_nodes, path_times, current_node_uuid)
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
