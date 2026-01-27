package models

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/lib/pq"
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
	flows.SessionStatusExpired:     SessionStatusExpired,
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
func InsertSessions(ctx context.Context, tx *sqlx.Tx, sessions []*Session) error {
	if len(sessions) == 0 {
		return nil
	}

	return insertDatabaseSessions(ctx, tx, sessions)
}

const sqlSelectSessionByUUID = `
SELECT uuid, contact_uuid, session_type, status, last_sprint_uuid, current_flow_uuid, output, created_on, ended_on, call_uuid
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

// deprecated
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

const sqlInterruptSessions = `
UPDATE flows_flowsession
   SET status = $2, ended_on = NOW(), current_flow_uuid = NULL
 WHERE uuid = ANY($1) AND status = 'W'`

const sqlInterruptSessionRuns = `
UPDATE flows_flowrun
   SET exited_on = NOW(), status = $2, modified_on = NOW()
 WHERE session_uuid = ANY($1) AND status IN ('A', 'W')`

const sqlInterruptSessionContacts = `
UPDATE contacts_contact 
   SET current_session_uuid = NULL, current_flow_id = NULL, modified_on = NOW() 
 WHERE id = ANY($1) AND current_session_uuid = ANY($2)`

// InterruptContacts interrupts the waiting sessions for the given contacts. It's on the caller to only call this for
// contacts that have waiting sessions and to ensure they are batched appropriately.
func InterruptContacts(ctx context.Context, tx *sqlx.Tx, contacts []*Contact, status flows.SessionStatus) error {
	dbStatus := sessionStatusMap[status]
	runStatus := RunStatus(dbStatus) // session status codes are subset of run status codes

	sessionUUIDs := make([]flows.SessionUUID, len(contacts))
	contactIDs := make([]ContactID, len(contacts))
	for i, c := range contacts {
		sessionUUIDs[i] = c.CurrentSessionUUID()
		contactIDs[i] = c.ID()

		c.currentSessionUUID = ""
		c.currentFlowID = 0
	}

	// first update the sessions themselves
	if _, err := tx.ExecContext(ctx, sqlInterruptSessions, pq.Array(sessionUUIDs), dbStatus); err != nil {
		return fmt.Errorf("error exiting sessions: %w", err)
	}

	// then the runs that belong to these sessions
	if _, err := tx.ExecContext(ctx, sqlInterruptSessionRuns, pq.Array(sessionUUIDs), runStatus); err != nil {
		return fmt.Errorf("error exiting session runs: %w", err)
	}

	// then the contacts from each session
	if _, err := tx.ExecContext(ctx, sqlInterruptSessionContacts, pq.Array(contactIDs), pq.Array(sessionUUIDs)); err != nil {
		return fmt.Errorf("error updating interrupted contacts: %w", err)
	}

	// finally any session related fires for these contacts
	if _, err := DeleteSessionFires(ctx, tx, contactIDs, true); err != nil {
		return fmt.Errorf("error deleting session contact fires: %w", err)
	}

	return nil
}

// SessionRef is a reference to a specific session for a contact. Since it's used for some task payloads, we use short
// JSON field names
type SessionRef struct {
	UUID      flows.SessionUUID `db:"session_uuid" json:"s"`
	ContactID ContactID         `db:"contact_id"   json:"c"`
}

const sqlSelectWaitingSessionsForFlow = `
SELECT DISTINCT contact_id, session_uuid FROM flows_flowrun WHERE status IN ('A', 'W') AND flow_id = $1 ORDER BY contact_id;`

// GetWaitingSessionsForFlow returns all waiting sessions for the given flow
func GetWaitingSessionsForFlow(ctx context.Context, db *sqlx.DB, flowID FlowID) ([]SessionRef, error) {
	var refs []SessionRef

	if err := db.SelectContext(ctx, &refs, sqlSelectWaitingSessionsForFlow, flowID); err != nil {
		return nil, fmt.Errorf("error selecting waiting sessions for flow #%d: %w", flowID, err)
	}

	return refs, nil
}

const sqlSelectWaitingSessionsForChannel = `
SELECT DISTINCT contact_id, session_uuid FROM ivr_call WHERE channel_id = $1 AND status = 'I' AND session_uuid IS NOT NULL ORDER BY contact_id;`

// GetWaitingSessionsForChannel returns all waiting sessions for the given channel (i.e. calls on IVR channel)
func GetWaitingSessionsForChannel(ctx context.Context, db *sqlx.DB, channelID ChannelID) ([]SessionRef, error) {
	var refs []SessionRef

	if err := db.SelectContext(ctx, &refs, sqlSelectWaitingSessionsForChannel, channelID); err != nil {
		return nil, fmt.Errorf("error selecting waiting sessions for channel %d: %w", channelID, err)
	}

	return refs, nil
}

type dbSession struct {
	UUID            flows.SessionUUID `db:"uuid"`
	ContactUUID     null.String       `db:"contact_uuid"`
	SessionType     FlowType          `db:"session_type"`
	Status          SessionStatus     `db:"status"`
	LastSprintUUID  null.String       `db:"last_sprint_uuid"`
	CurrentFlowUUID null.String       `db:"current_flow_uuid"`
	CallUUID        null.String       `db:"call_uuid"`
	Output          null.String       `db:"output"`
	CreatedOn       time.Time         `db:"created_on"`
	EndedOn         *time.Time        `db:"ended_on"`
}

const sqlInsertWaitingSessionDB = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output,  created_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output, :created_on, :call_uuid)`

const sqlInsertEndedSessionDB = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output,  created_on,  ended_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output, :created_on, :ended_on, :call_uuid)`

func insertDatabaseSessions(ctx context.Context, tx *sqlx.Tx, sessions []*Session) error {
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
