package models

import (
	"context"
	"crypto/md5"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/aws/s3x"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

type SessionStatus string

const (
	SessionStatusWaiting     SessionStatus = "W"
	SessionStatusCompleted   SessionStatus = "C"
	SessionStatusExpired     SessionStatus = "X"
	SessionStatusInterrupted SessionStatus = "I"
	SessionStatusFailed      SessionStatus = "F"

	storageTSFormat = "20060102T150405.999Z"
)

var sessionStatusMap = map[flows.SessionStatus]SessionStatus{
	flows.SessionStatusWaiting:   SessionStatusWaiting,
	flows.SessionStatusCompleted: SessionStatusCompleted,
	flows.SessionStatusFailed:    SessionStatusFailed,
}

// Session is the mailroom type for a FlowSession
type Session struct {
	s struct {
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
}

func (s *Session) UUID() flows.SessionUUID          { return s.s.UUID }
func (s *Session) ContactUUID() flows.ContactUUID   { return flows.ContactUUID(s.s.ContactUUID) }
func (s *Session) SessionType() FlowType            { return s.s.SessionType }
func (s *Session) Status() SessionStatus            { return s.s.Status }
func (s *Session) LastSprintUUID() flows.SprintUUID { return flows.SprintUUID(s.s.LastSprintUUID) }
func (s *Session) CurrentFlowUUID() assets.FlowUUID { return assets.FlowUUID(s.s.CurrentFlowUUID) }
func (s *Session) CallUUID() flows.CallUUID         { return flows.CallUUID(s.s.CallUUID) }
func (s *Session) Output() string                   { return string(s.s.Output) }
func (s *Session) OutputURL() string                { return string(s.s.OutputURL) }
func (s *Session) CreatedOn() time.Time             { return s.s.CreatedOn }
func (s *Session) EndedOn() *time.Time              { return s.s.EndedOn }

// StoragePath returns the path for the session
func (s *Session) StoragePath(orgID OrgID, contactUUID flows.ContactUUID) string {
	ts := s.CreatedOn().UTC().Format(storageTSFormat)

	// example output: orgs/1/c/20a5/20a5534c-b2ad-4f18-973a-f1aa3b4e6c74/20060102T150405.123Z_session_8a7fc501-177b-4567-a0aa-81c48e6de1c5_51df83ac21d3cf136d8341f0b11cb1a7.json"
	return path.Join(
		"orgs",
		fmt.Sprintf("%d", orgID),
		"c",
		string(contactUUID[:4]),
		string(contactUUID),
		fmt.Sprintf("%s_session_%s_%s.json", ts, s.UUID(), s.OutputMD5()),
	)
}

// OutputMD5 returns the md5 of the passed in session
func (s *Session) OutputMD5() string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s.s.Output)))
}

// EngineSession creates a flow session for the passed in session object
func (s *Session) EngineSession(ctx context.Context, rt *runtime.Runtime, sa flows.SessionAssets, env envs.Environment, contact *flows.Contact, call *flows.Call) (flows.Session, error) {
	session, err := goflow.Engine(rt).ReadSession(sa, []byte(s.s.Output), env, contact, call, assets.IgnoreMissing)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal session: %w", err)
	}

	return session, nil
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

const sqlUpdateSessionS3 = `
UPDATE 
	flows_flowsession
SET 
	output = NULL,
	output_url = :output_url,
	status = :status, 
	last_sprint_uuid = :last_sprint_uuid,
	ended_on = :ended_on,
	current_flow_uuid = :current_flow_uuid
WHERE 
	uuid = :uuid`

// Update updates the session based on the state passed in from our engine session, this also takes care of applying any event hooks
func (s *Session) Update(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, fs flows.Session, sprint flows.Sprint, contact *Contact) error {
	s.s.Output = null.String(jsonx.MustMarshal(fs))
	s.s.Status = sessionStatusMap[fs.Status()]
	s.s.LastSprintUUID = null.String(sprint.UUID())

	if s.s.Status != SessionStatusWaiting {
		now := time.Now()
		s.s.EndedOn = &now
	}

	// run through our runs to figure out our current flow
	s.s.CurrentFlowUUID = null.NullString

	for _, r := range fs.Runs() {
		// if this run is waiting, save it as the current flow
		if r.Status() == flows.RunStatusWaiting && r.Flow() != nil {
			s.s.CurrentFlowUUID = null.String(r.FlowReference().UUID)
			break
		}
	}

	// the SQL statement we'll use to update this session
	updateSQL := sqlUpdateSessionDB

	// if writing to S3, do so
	if rt.Config.SessionStorage == "s3" {
		if err := writeSessionsToStorage(ctx, rt, oa.OrgID(), []*Session{s}, []*Contact{contact}); err != nil {
			slog.Error("error writing session to s3", "error", err)
		}

		// don't write output in our SQL
		updateSQL = sqlUpdateSessionS3
	}

	// write our new session state to the db
	if _, err := tx.NamedExecContext(ctx, updateSQL, s.s); err != nil {
		return fmt.Errorf("error updating session: %w", err)
	}

	return nil
}

// NewSession creates a db session from the passed in engine session
func NewSession(oa *OrgAssets, fs flows.Session, sprint flows.Sprint, call *Call) *Session {
	session := &Session{}
	s := &session.s
	s.UUID = fs.UUID()
	s.ContactUUID = null.String(fs.Contact().UUID())
	s.Status = sessionStatusMap[fs.Status()]
	s.LastSprintUUID = null.String(sprint.UUID())
	s.SessionType = flowTypeMapping[fs.Type()]
	s.Output = null.String(jsonx.MustMarshal(fs))
	s.CreatedOn = fs.CreatedOn()

	if call != nil {
		s.CallUUID = null.String(call.UUID())
	}

	if s.Status != SessionStatusWaiting {
		now := time.Now()
		s.EndedOn = &now
	}

	for _, r := range fs.Runs() {
		// if this run is waiting, save it as the current flow
		if r.Status() == flows.RunStatusWaiting && r.Flow() != nil {
			s.CurrentFlowUUID = null.String(r.FlowReference().UUID)
			break
		}
	}

	return session
}

const sqlInsertWaitingSessionDB = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output,  created_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output, :created_on, :call_uuid)`

const sqlInsertWaitingSessionS3 = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output_url,  created_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output_url, :created_on, :call_uuid)`

const sqlInsertEndedSessionDB = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output,  created_on,  ended_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output, :created_on, :ended_on, :call_uuid)`

const sqlInsertEndedSessionS3 = `
INSERT INTO
	flows_flowsession( uuid,  contact_uuid,  session_type,  status,  last_sprint_uuid,  current_flow_uuid,  output_url,  created_on,  ended_on,  call_uuid)
               VALUES(:uuid, :contact_uuid, :session_type, :status, :last_sprint_uuid, :current_flow_uuid, :output_url, :created_on, :ended_on, :call_uuid)`

// InsertSessions inserts sessions and their runs into the database
func InsertSessions(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, sessions []*Session, contacts []*Contact) error {
	if len(sessions) == 0 {
		return nil
	}

	// split into waiting and ended sessions
	waitingSessionsI := make([]any, 0, len(sessions))
	endedSessionsI := make([]any, 0, len(sessions))
	for _, s := range sessions {
		if s.Status() == SessionStatusWaiting {
			waitingSessionsI = append(waitingSessionsI, &s.s)
		} else {
			endedSessionsI = append(endedSessionsI, &s.s)
		}
	}

	// the SQL we'll use to do our insert of sessions
	insertEndedSQL := sqlInsertEndedSessionDB
	insertWaitingSQL := sqlInsertWaitingSessionDB

	// if writing our sessions to S3, do so
	if rt.Config.SessionStorage == "s3" {
		if err := writeSessionsToStorage(ctx, rt, oa.OrgID(), sessions, contacts); err != nil {
			return fmt.Errorf("error writing sessions to storage: %w", err)
		}

		insertEndedSQL = sqlInsertEndedSessionS3
		insertWaitingSQL = sqlInsertWaitingSessionS3
	}

	// insert our ended sessions first
	if err := BulkQuery(ctx, "insert ended sessions", tx, insertEndedSQL, endedSessionsI); err != nil {
		return fmt.Errorf("error inserting ended sessions: %w", err)
	}
	// insert waiting sessions
	if err := BulkQuery(ctx, "insert waiting sessions", tx, insertWaitingSQL, waitingSessionsI); err != nil {
		return fmt.Errorf("error inserting waiting sessions: %w", err)
	}

	return nil
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
	session := &Session{}

	if err := rows.StructScan(&session.s); err != nil {
		return nil, fmt.Errorf("error scanning session: %w", err)
	}

	// ignore and log if this session somehow isn't a waiting session for this contact
	if session.s.Status != SessionStatusWaiting || (session.ContactUUID() != "" && session.ContactUUID() != fc.UUID()) {
		slog.Error("current session for contact isn't a waiting session", "session", uuid, "contact", fc.UUID())
		return nil, nil
	}

	// load our output from storage if necessary
	if session.OutputURL() != "" {
		// strip just the path out of our output URL
		u, err := url.Parse(session.OutputURL())
		if err != nil {
			return nil, fmt.Errorf("error parsing output URL: %s: %w", session.OutputURL(), err)
		}
		key := strings.TrimPrefix(u.Path, "/")

		start := time.Now()

		_, output, err := rt.S3.GetObject(ctx, rt.Config.S3SessionsBucket, key)
		if err != nil {
			return nil, fmt.Errorf("error reading session from s3 bucket=%s key=%s: %w", rt.Config.S3SessionsBucket, key, err)
		}

		slog.Debug("loaded session from storage", "elapsed", time.Since(start), "output_url", session.OutputURL())
		session.s.Output = null.String(output)
	}

	return session, nil
}

// WriteSessionsToStorage writes the outputs of the passed in sessions to our storage (S3), updating the
// output_url for each on success. Failure of any will cause all to fail.
func writeSessionsToStorage(ctx context.Context, rt *runtime.Runtime, orgID OrgID, sessions []*Session, contacts []*Contact) error {
	start := time.Now()

	uploads := make([]*s3x.Upload, len(sessions))
	for i, s := range sessions {
		uploads[i] = &s3x.Upload{
			Bucket:      rt.Config.S3SessionsBucket,
			Key:         s.StoragePath(orgID, contacts[i].UUID()),
			Body:        []byte(s.Output()),
			ContentType: "application/json",
			ACL:         types.ObjectCannedACLPrivate,
		}
	}

	err := rt.S3.BatchPut(ctx, uploads, 32)
	if err != nil {
		return fmt.Errorf("error writing sessions to storage: %w", err)
	}

	for i, s := range sessions {
		s.s.OutputURL = null.String(uploads[i].URL)
	}

	slog.Debug("wrote sessions to s3", "elapsed", time.Since(start), "count", len(sessions))
	return nil
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

// InterruptSessionsForContacts interrupts any waiting sessions for the given contacts, returning the number of sessions interrupted
func InterruptSessionsForContacts(ctx context.Context, db *sqlx.DB, contactIDs []ContactID) (int, error) {
	sessionUUIDs, err := getWaitingSessionsForContacts(ctx, db, contactIDs)
	if err != nil {
		return 0, err
	}

	if err := ExitSessions(ctx, db, sessionUUIDs, SessionStatusInterrupted); err != nil {
		return 0, fmt.Errorf("error exiting sessions: %w", err)
	}

	return len(sessionUUIDs), nil
}

// InterruptSessionsForContactsTx interrupts any waiting sessions for the given contacts inside the given transaction.
// This version is used for interrupting during flow starts where contacts are already batched and we have an open transaction.
func InterruptSessionsForContactsTx(ctx context.Context, tx *sqlx.Tx, contactIDs []ContactID) error {
	sessionUUIDs, err := getWaitingSessionsForContacts(ctx, tx, contactIDs)
	if err != nil {
		return err
	}

	if len(sessionUUIDs) > 0 {
		if err := exitSessionBatch(ctx, tx, sessionUUIDs, SessionStatusInterrupted); err != nil {
			return fmt.Errorf("error exiting sessions: %w", err)
		}
	}

	return nil
}

const sqlSelectWaitingSessionsForContacts = `
SELECT current_session_uuid FROM contacts_contact WHERE id = ANY($1) AND current_session_uuid IS NOT NULL`

func getWaitingSessionsForContacts(ctx context.Context, db DBorTx, contactIDs []ContactID) ([]flows.SessionUUID, error) {
	sessionUUIDs := make([]flows.SessionUUID, 0, len(contactIDs))

	if err := db.SelectContext(ctx, &sessionUUIDs, sqlSelectWaitingSessionsForContacts, pq.Array(contactIDs)); err != nil {
		return nil, fmt.Errorf("error selecting current sessions for contacts: %w", err)
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
