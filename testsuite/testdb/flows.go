package testdb

import (
	"os"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

type Flow struct {
	ID   models.FlowID
	UUID assets.FlowUUID
}

func (f *Flow) Load(rt *runtime.Runtime, oa *models.OrgAssets) *models.Flow {
	flow, err := oa.FlowByID(f.ID)
	if err != nil {
		panic(err)
	}
	return flow
}

func (f *Flow) Reference() *assets.FlowReference {
	return &assets.FlowReference{UUID: f.UUID, Name: ""}
}

// InsertFlow inserts a flow
func InsertFlow(rt *runtime.Runtime, org *Org, definition []byte) *Flow {
	uuid, err := jsonparser.GetString(definition, "uuid")
	if err != nil {
		panic(err)
	}
	name, err := jsonparser.GetString(definition, "name")
	if err != nil {
		panic(err)
	}

	var id models.FlowID
	must(rt.DB.Get(&id,
		`INSERT INTO flows_flow(org_id, uuid, name, flow_type, version_number, base_language, expires_after_minutes, ignore_triggers, has_issues, is_active, is_archived, is_system, created_by_id, created_on, modified_by_id, modified_on, saved_on, saved_by_id) 
		VALUES($1, $2, $3, 'M', '13.1.0', 'eng', 10, FALSE, FALSE, TRUE, FALSE, FALSE, $4, NOW(), $4, NOW(), NOW(), $4) RETURNING id`, org.ID, uuid, name, Admin.ID,
	))

	rt.DB.MustExec(`INSERT INTO flows_flowrevision(flow_id, definition, spec_version, revision, created_by_id, created_on) 
	VALUES($1, $2, '13.1.0', 1, $3, NOW())`, id, definition, Admin.ID)

	return &Flow{ID: id, UUID: assets.FlowUUID(uuid)}
}

func ImportFlows(rt *runtime.Runtime, org *Org, path string) []*Flow {
	assetsJSON, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	flowsJSON, _, _, err := jsonparser.Get(assetsJSON, "flows")
	if err != nil {
		panic(err)
	}

	flows := []*Flow{}

	_, err = jsonparser.ArrayEach(flowsJSON, func(flowJSON []byte, dataType jsonparser.ValueType, offset int, err error) {
		flow := InsertFlow(rt, org, flowJSON)
		flows = append(flows, flow)
	})
	if err != nil {
		panic(err)
	}

	return flows
}

// InsertFlowStart inserts a flow start
func InsertFlowStart(rt *runtime.Runtime, org *Org, user *User, flow *Flow, contacts []*Contact) models.StartID {
	var id models.StartID
	must(rt.DB.Get(&id,
		`INSERT INTO flows_flowstart(uuid, org_id, flow_id, start_type, exclusions, created_on, modified_on, contact_count, status, created_by_id)
		 VALUES($1, $2, $3, 'M', '{}', NOW(), NOW(), 2, 'P', $4) RETURNING id`, uuids.NewV4(), org.ID, flow.ID, user.ID,
	))

	for _, c := range contacts {
		rt.DB.MustExec(`INSERT INTO flows_flowstart_contacts(flowstart_id, contact_id) VALUES($1, $2)`, id, c.ID)
	}

	return id
}

// InsertFlowSession inserts a flow session
func InsertFlowSession(rt *runtime.Runtime, contact *Contact, sessionType models.FlowType, status models.SessionStatus, currentFlow *Flow, callID models.CallID) flows.SessionUUID {
	now := time.Now()
	uuid := flows.NewSessionUUID()

	var endedOn *time.Time
	if status != models.SessionStatusWaiting {
		endedOn = &now
	}

	rt.DB.MustExec(
		`INSERT INTO flows_flowsession(uuid, contact_id, status, output, created_on, session_type, current_flow_id, call_id, ended_on) 
		 VALUES($1, $2, $3, '{}', NOW(), $4, $5, $6, $7) RETURNING id`, uuid, contact.ID, status, sessionType, currentFlow.ID, callID, endedOn,
	)
	return uuid
}

// InsertWaitingSession inserts a waiting flow session with a corresponding waiting run, and updates the contact
func InsertWaitingSession(rt *runtime.Runtime, org *Org, contact *Contact, sessionType models.FlowType, currentFlow *Flow, callID models.CallID) flows.SessionUUID {
	uuid := flows.NewSessionUUID()

	rt.DB.MustExec(
		`INSERT INTO flows_flowsession(uuid, contact_id, status, last_sprint_uuid, output, created_on, session_type, current_flow_id, call_id) 
		 VALUES($1, $2, 'W', $3, '{"status":"waiting"}', NOW(), $4, $5, $6) RETURNING id`, uuid, contact.ID, uuids.NewV4(), sessionType, currentFlow.ID, callID,
	)

	InsertFlowRun(rt, org, uuid, contact, currentFlow, models.RunStatusWaiting, flows.NodeUUID(uuids.NewV4()))

	rt.DB.MustExec(`UPDATE contacts_contact SET current_session_uuid = $2, current_flow_id = $3 WHERE id = $1`, contact.ID, uuid, currentFlow.ID)
	return uuid
}

// InsertFlowRun inserts a flow run
func InsertFlowRun(rt *runtime.Runtime, org *Org, sessionUUID flows.SessionUUID, contact *Contact, flow *Flow, status models.RunStatus, currentNodeUUID flows.NodeUUID) models.FlowRunID {
	now := time.Now()

	var exitedOn *time.Time
	if status != models.RunStatusActive && status != models.RunStatusWaiting {
		exitedOn = &now
	}

	var id models.FlowRunID
	must(rt.DB.Get(&id,
		`INSERT INTO flows_flowrun(uuid, org_id, session_uuid, contact_id, flow_id, status, responded, current_node_uuid, created_on, modified_on, exited_on) 
		 VALUES($1, $2, $3, $4, $5, $6, TRUE, $7, NOW(), NOW(), $8) RETURNING id`, uuids.NewV4(), org.ID, sessionUUID, contact.ID, flow.ID, status, null.String(currentNodeUUID), exitedOn,
	))
	return id
}
