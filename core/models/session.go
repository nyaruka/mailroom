package models

import (
	"context"
	"crypto/md5"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

type SessionStatus string

const (
	SessionStatusWaiting     SessionStatus = "W"
	SessionStatusCompleted   SessionStatus = "C"
	SessionStatusExpired     SessionStatus = "X"
	SessionStatusInterrupted SessionStatus = "I"
	SessionStatusFailed      SessionStatus = "F"
)

var sessionStatusMap = map[flows.SessionStatus]SessionStatus{
	flows.SessionStatusWaiting:     SessionStatusWaiting,
	flows.SessionStatusCompleted:   SessionStatusCompleted,
	flows.SessionStatusFailed:      SessionStatusFailed,
	flows.SessionStatusInterrupted: SessionStatusInterrupted,
}

// Session is the mailroom type for a FlowSession
type Session struct {
	UUID            flows.SessionUUID
	ContactUUID     flows.ContactUUID
	SessionType     FlowType
	Status          SessionStatus
	LastSprintUUID  flows.SprintUUID
	CurrentFlowUUID assets.FlowUUID
	CallUUID        flows.CallUUID
	Output          []byte
	CreatedOn       time.Time
	EndedOn         *time.Time
}

// NewSession creates a db session from the passed in engine session
func NewSession(oa *OrgAssets, fs flows.Session, sprint flows.Sprint, call *Call) *Session {
	s := &Session{}
	s.UUID = fs.UUID()
	s.ContactUUID = fs.Contact().UUID()
	s.Status = sessionStatusMap[fs.Status()]
	s.LastSprintUUID = sprint.UUID()
	s.SessionType = flowTypeMapping[fs.Type()]
	s.Output = jsonx.MustMarshal(fs)
	s.CreatedOn = fs.CreatedOn()

	if call != nil {
		s.CallUUID = call.UUID()
	}

	if s.Status != SessionStatusWaiting {
		now := time.Now()
		s.EndedOn = &now
	}

	for _, r := range fs.Runs() {
		// if this run is waiting, save it as the current flow
		if r.Status() == flows.RunStatusWaiting && r.Flow() != nil {
			s.CurrentFlowUUID = r.FlowReference().UUID
			break
		}
	}

	return s
}

// EngineSession creates a flow session for the passed in session object
func (s *Session) EngineSession(ctx context.Context, rt *runtime.Runtime, sa flows.SessionAssets, env envs.Environment, contact *flows.Contact, call *flows.Call) (flows.Session, error) {
	session, err := goflow.Engine(rt).ReadSession(sa, []byte(s.Output), env, contact, call, assets.IgnoreMissing)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal session: %w", err)
	}

	return session, nil
}

// Update updates the session based on the state passed in from our engine session, this also takes care of applying any event hooks
func (s *Session) Update(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, fs flows.Session, sprint flows.Sprint, contact *Contact) error {
	s.Output = jsonx.MustMarshal(fs)
	s.Status = sessionStatusMap[fs.Status()]
	s.LastSprintUUID = sprint.UUID()

	if s.Status != SessionStatusWaiting {
		now := time.Now()
		s.EndedOn = &now
	}

	// run through our runs to figure out our current flow
	s.CurrentFlowUUID = ""

	for _, r := range fs.Runs() {
		// if this run is waiting, save it as the current flow
		if r.Status() == flows.RunStatusWaiting && r.Flow() != nil {
			s.CurrentFlowUUID = r.FlowReference().UUID
			break
		}
	}

	return updateDatabaseSession(ctx, rt, tx, oa, s, contact)
}

// InsertSessions inserts sessions and their runs into the database
func InsertSessions(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, sessions []*Session, contacts []*Contact) error {
	if len(sessions) == 0 {
		return nil
	}

	return insertDatabaseSessions(ctx, rt, tx, oa, sessions, contacts)
}

const sqlSelectSessionByUUID = `
SELECT uuid, contact_uuid, session_type, status, last_sprint_uuid, current_flow_uuid, output, output_url, created_on, ended_on, call_uuid
  FROM flows_flowsession fs
 WHERE uuid = $1`

// GetWaitingSessionForContact returns the waiting session for the passed in contact, if any
func GetWaitingSessionForContact(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, fc *flows.Contact, uuid flows.SessionUUID) (*Session, error) {
	rows, err := rt.DB.QueryxContext(ctx, sqlSelectSessionByUUID, uuid)
	if err != nil {
		return nil, fmt.Errorf("error selecting session %s: %w", uuid, err)
	}
	defer rows.Close()

	// no rows? no sessions!
	if !rows.Next() {
		return nil, nil
	}

	// scan in our session
	dbs := &dbSession{}
	if err := rows.StructScan(dbs); err != nil {
		return nil, fmt.Errorf("error scanning session: %w", err)
	}

	session := &Session{
		UUID:            dbs.UUID,
		ContactUUID:     flows.ContactUUID(dbs.ContactUUID),
		SessionType:     dbs.SessionType,
		Status:          dbs.Status,
		LastSprintUUID:  flows.SprintUUID(dbs.LastSprintUUID),
		CurrentFlowUUID: assets.FlowUUID(dbs.CurrentFlowUUID),
		CallUUID:        flows.CallUUID(dbs.CallUUID),
		Output:          []byte(dbs.Output),
		CreatedOn:       dbs.CreatedOn,
		EndedOn:         dbs.EndedOn,
	}

	// ignore and log if this session somehow isn't a waiting session for this contact
	if dbs.Output == "" || session.Status != SessionStatusWaiting || (session.ContactUUID != "" && session.ContactUUID != fc.UUID()) {
		slog.Error("current session for contact isn't a waiting session with output", "session", uuid, "contact", fc.UUID())
		return nil, nil
	}

	return session, nil
}

// ExitSessions exits waiting sessions and their runs
func ExitSessions(ctx context.Context, db *sqlx.DB, uuids []flows.SessionUUID, status SessionStatus) error {
	// split into batches and exit each batch in a transaction
	for batch := range slices.Chunk(uuids, 100) {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("error starting transaction to interrupt sessions: %w", err)
		}

		if err := exitSessionBatch(ctx, tx, batch, status); err != nil {
			tx.Rollback()
			return fmt.Errorf("error interrupting batch of sessions: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("error committing session interrupts: %w", err)
		}
	}

	return nil
}

const sqlExitSessions = `
   UPDATE flows_flowsession
      SET status = $2, ended_on = NOW(), current_flow_uuid = NULL
    WHERE uuid = ANY($1) AND status = 'W'
RETURNING contact_uuid`

// TODO instead of having an index on session_uuid.. rework this to fetch the sessions and extract a list of run uuids?
const sqlExitSessionRuns = `
UPDATE flows_flowrun
   SET exited_on = NOW(), status = $2, modified_on = NOW()
 WHERE session_uuid = ANY($1) AND status IN ('A', 'W')`

const sqlExitSessionContacts = `
   UPDATE contacts_contact 
      SET current_session_uuid = NULL, current_flow_id = NULL, modified_on = NOW() 
    WHERE uuid = ANY($1) AND current_session_uuid = ANY($2)
RETURNING id`

// exits sessions and their runs inside the given transaction
func exitSessionBatch(ctx context.Context, tx *sqlx.Tx, uuids []flows.SessionUUID, status SessionStatus) error {
	runStatus := RunStatus(status) // session status codes are subset of run status codes
	contactUUIDs := make([]flows.ContactUUID, 0, len(uuids))

	// first update the sessions themselves and get the contact UUIDs
	if err := tx.SelectContext(ctx, &contactUUIDs, sqlExitSessions, pq.Array(uuids), status); err != nil {
		return fmt.Errorf("error exiting sessions: %w", err)
	}

	// then the runs that belong to these sessions
	if _, err := tx.ExecContext(ctx, sqlExitSessionRuns, pq.Array(uuids), runStatus); err != nil {
		return fmt.Errorf("error exiting session runs: %w", err)
	}

	// and finally the contacts from each session
	contactIDs := make([]ContactID, 0, len(contactUUIDs))
	if err := tx.SelectContext(ctx, &contactIDs, sqlExitSessionContacts, pq.Array(contactUUIDs), pq.Array(uuids)); err != nil {
		return fmt.Errorf("error exiting sessions: %w", err)
	}

	// delete any session related fires for these contacts
	if _, err := DeleteSessionFires(ctx, tx, contactIDs, true); err != nil {
		return fmt.Errorf("error deleting session contact fires: %w", err)
	}

	return nil
}

// InterruptContacts interrupts any waiting sessions for the given contacts which are assumed to be batched.
func InterruptContacts(ctx context.Context, tx *sqlx.Tx, contacts map[ContactID]flows.SessionStatus) error {
	// re-org into contact IDs by status
	statuses := make(map[flows.SessionStatus][]ContactID)
	for contactID, status := range contacts {
		statuses[status] = append(statuses[status], contactID)
	}

	for status, contactIDs := range statuses {
		sessionUUIDs, err := getWaitingSessionsForContacts(ctx, tx, contactIDs)
		if err != nil {
			return err
		}

		if len(sessionUUIDs) > 0 {
			if err := exitSessionBatch(ctx, tx, slices.Collect(maps.Values(sessionUUIDs)), sessionStatusMap[status]); err != nil {
				return fmt.Errorf("error exiting sessions: %w", err)
			}
		}
	}

	return nil
}

const sqlSelectWaitingSessionsForContacts = `
SELECT id, current_session_uuid FROM contacts_contact WHERE id = ANY($1) AND current_session_uuid IS NOT NULL`

func getWaitingSessionsForContacts(ctx context.Context, db DBorTx, contactIDs []ContactID) (map[ContactID]flows.SessionUUID, error) {
	rows, err := db.QueryContext(ctx, sqlSelectWaitingSessionsForContacts, pq.Array(contactIDs))
	if err != nil {
		return nil, fmt.Errorf("error selecting current sessions for contacts: %w", err)
	}

	sessionUUIDs := make(map[ContactID]flows.SessionUUID, len(contactIDs))
	if err = dbutil.ScanAllMap(rows, sessionUUIDs); err != nil {
		return nil, fmt.Errorf("error scanning current sessions for contacts: %w", err)
	}

	return sessionUUIDs, nil
}

const sqlSelectWaitingSessionsForChannel = `
SELECT session_uuid 
  FROM ivr_call 
 WHERE channel_id = $1 AND status NOT IN ('D', 'F') AND session_uuid IS NOT NULL;`

// InterruptSessionsForChannel interrupts any waiting sessions with calls on the given channel
func InterruptSessionsForChannel(ctx context.Context, db *sqlx.DB, channelID ChannelID) error {
	sessionUUIDs := make([]flows.SessionUUID, 0, 10)

	err := db.SelectContext(ctx, &sessionUUIDs, sqlSelectWaitingSessionsForChannel, channelID)
	if err != nil {
		return fmt.Errorf("error selecting waiting sessions for channel %d: %w", channelID, err)
	}

	if err := ExitSessions(ctx, db, sessionUUIDs, SessionStatusInterrupted); err != nil {
		return fmt.Errorf("error interrupting sessions for channel: %w", err)
	}

	return nil
}

const sqlSelectWaitingSessionsForFlows = `
SELECT DISTINCT session_uuid
  FROM flows_flowrun
 WHERE status IN ('A', 'W') AND flow_id = ANY($1);`

// InterruptSessionsForFlows interrupts any waiting sessions currently in the given flows
func InterruptSessionsForFlows(ctx context.Context, db *sqlx.DB, flowIDs []FlowID) error {
	var sessionUUIDs []flows.SessionUUID

	err := db.SelectContext(ctx, &sessionUUIDs, sqlSelectWaitingSessionsForFlows, pq.Array(flowIDs))
	if err != nil {
		return fmt.Errorf("error selecting waiting sessions for flows: %w", err)
	}

	if err := ExitSessions(ctx, db, sessionUUIDs, SessionStatusInterrupted); err != nil {
		return fmt.Errorf("error interrupting sessions: %w", err)
	}

	return nil
}

const (
	storageTSFormat = "20060102T150405.999Z"
)

type dbSession struct {
	UUID            flows.SessionUUID `db:"uuid"`
	ContactUUID     null.String       `db:"contact_uuid"`
	SessionType     FlowType          `db:"session_type"`
	Status          SessionStatus     `db:"status"`
	LastSprintUUID  null.String       `db:"last_sprint_uuid"`
	CurrentFlowUUID null.String       `db:"current_flow_uuid"`
	CallUUID        null.String       `db:"call_uuid"`
	Output          null.String       `db:"output"`
	OutputURL       null.String       `db:"output_url"`
	CreatedOn       time.Time         `db:"created_on"`
	EndedOn         *time.Time        `db:"ended_on"`
}

// StoragePath returns the path for the session
func (s *dbSession) StoragePath(orgID OrgID, contactUUID flows.ContactUUID) string {
	ts := s.CreatedOn.UTC().Format(storageTSFormat)

	// example output: orgs/1/c/20a5/20a5534c-b2ad-4f18-973a-f1aa3b4e6c74/20060102T150405.123Z_session_8a7fc501-177b-4567-a0aa-81c48e6de1c5_51df83ac21d3cf136d8341f0b11cb1a7.json"
	return path.Join(
		"orgs",
		fmt.Sprintf("%d", orgID),
		"c",
		string(contactUUID[:4]),
		string(contactUUID),
		fmt.Sprintf("%s_session_%s_%s.json", ts, s.UUID, s.OutputMD5()),
	)
}

// OutputMD5 returns the md5 of the passed in session
func (s *dbSession) OutputMD5() string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s.Output)))
}

const sqlInsertWaitingSessionDB = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output,  created_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output, :created_on, :call_uuid)`

const sqlInsertEndedSessionDB = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output,  created_on,  ended_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output, :created_on, :ended_on, :call_uuid)`

func insertDatabaseSessions(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, sessions []*Session, contacts []*Contact) error {
	dbss := make([]*dbSession, len(sessions))
	for i, s := range sessions {
		dbss[i] = &dbSession{
			UUID:            s.UUID,
			ContactUUID:     null.String(s.ContactUUID),
			SessionType:     s.SessionType,
			Status:          s.Status,
			LastSprintUUID:  null.String(s.LastSprintUUID),
			CurrentFlowUUID: null.String(s.CurrentFlowUUID),
			CallUUID:        null.String(s.CallUUID),
			Output:          null.String(s.Output),
			CreatedOn:       s.CreatedOn,
			EndedOn:         s.EndedOn,
		}
	}

	// split into waiting and ended sessions
	waitingSessions := make([]*dbSession, 0, len(sessions))
	endedSessions := make([]*dbSession, 0, len(sessions))
	for _, s := range dbss {
		if s.Status == SessionStatusWaiting {
			waitingSessions = append(waitingSessions, s)
		} else {
			endedSessions = append(endedSessions, s)
		}
	}

	// insert our ended sessions first
	if err := BulkQuery(ctx, "insert ended sessions", tx, sqlInsertEndedSessionDB, endedSessions); err != nil {
		return fmt.Errorf("error inserting ended sessions: %w", err)
	}
	// insert waiting sessions
	if err := BulkQuery(ctx, "insert waiting sessions", tx, sqlInsertWaitingSessionDB, waitingSessions); err != nil {
		return fmt.Errorf("error inserting waiting sessions: %w", err)
	}

	return nil
}

const sqlUpdateSessionDB = `
UPDATE 
	flows_flowsession
SET 
	output = :output, 
	output_url = NULL,
	status = :status,
	last_sprint_uuid = :last_sprint_uuid,
	ended_on = :ended_on,
	current_flow_uuid = :current_flow_uuid
WHERE 
	uuid = :uuid`

func updateDatabaseSession(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, s *Session, contact *Contact) error {
	dbs := &dbSession{
		UUID:            s.UUID,
		ContactUUID:     null.String(s.ContactUUID),
		SessionType:     s.SessionType,
		Status:          s.Status,
		LastSprintUUID:  null.String(s.LastSprintUUID),
		CurrentFlowUUID: null.String(s.CurrentFlowUUID),
		CallUUID:        null.String(s.CallUUID),
		Output:          null.String(s.Output),
		CreatedOn:       s.CreatedOn,
		EndedOn:         s.EndedOn,
	}

	// write our new session state to the db
	if _, err := tx.NamedExecContext(ctx, sqlUpdateSessionDB, dbs); err != nil {
		return fmt.Errorf("error updating session: %w", err)
	}

	return nil
}
