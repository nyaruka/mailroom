package models

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/aws/s3x"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
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

	// SessionExpires is the default *overall* expiration time for a session
	SessionExpires = 30 * 24 * time.Hour

	storageTSFormat = "20060102T150405.999Z"
)

var sessionStatusMap = map[flows.SessionStatus]SessionStatus{
	flows.SessionStatusWaiting:   SessionStatusWaiting,
	flows.SessionStatusCompleted: SessionStatusCompleted,
	flows.SessionStatusFailed:    SessionStatusFailed,
}

type SessionCommitHook func(context.Context, *sqlx.Tx, *redis.Pool, *OrgAssets, []*Session) error

// Session is the mailroom type for a FlowSession
type Session struct {
	s struct {
		UUID           flows.SessionUUID `db:"uuid"`
		SessionType    FlowType          `db:"session_type"`
		Status         SessionStatus     `db:"status"`
		LastSprintUUID null.String       `db:"last_sprint_uuid"`
		Output         null.String       `db:"output"`
		OutputURL      null.String       `db:"output_url"`
		ContactID      ContactID         `db:"contact_id"`
		CreatedOn      time.Time         `db:"created_on"`
		EndedOn        *time.Time        `db:"ended_on"`
		CurrentFlowID  FlowID            `db:"current_flow_id"`
		CallID         CallID            `db:"call_id"`
	}

	// time after our last message is sent that we should timeout
	timeout *time.Duration

	runs     []*FlowRun
	seenRuns map[flows.RunUUID]time.Time

	// we keep around a reference to the sprint associated with this session
	sprint flows.Sprint
}

func (s *Session) UUID() flows.SessionUUID          { return s.s.UUID }
func (s *Session) ContactID() ContactID             { return s.s.ContactID }
func (s *Session) SessionType() FlowType            { return s.s.SessionType }
func (s *Session) Status() SessionStatus            { return s.s.Status }
func (s *Session) LastSprintUUID() flows.SprintUUID { return flows.SprintUUID(s.s.LastSprintUUID) }
func (s *Session) Output() string                   { return string(s.s.Output) }
func (s *Session) OutputURL() string                { return string(s.s.OutputURL) }
func (s *Session) CreatedOn() time.Time             { return s.s.CreatedOn }
func (s *Session) EndedOn() *time.Time              { return s.s.EndedOn }
func (s *Session) CurrentFlowID() FlowID            { return s.s.CurrentFlowID }
func (s *Session) CallID() CallID                   { return s.s.CallID }

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

// Runs returns our flow run
func (s *Session) Runs() []*FlowRun {
	return s.runs
}

// Sprint returns the sprint associated with this session
func (s *Session) Sprint() flows.Sprint {
	return s.sprint
}

// Timeout returns the amount of time after our last message sends that we should timeout
func (s *Session) Timeout() *time.Duration {
	return s.timeout
}

// OutputMD5 returns the md5 of the passed in session
func (s *Session) OutputMD5() string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s.s.Output)))
}

// FlowSession creates a flow session for the passed in session object. It also populates the runs we know about
func (s *Session) FlowSession(ctx context.Context, rt *runtime.Runtime, sa flows.SessionAssets, env envs.Environment) (flows.Session, error) {
	session, err := goflow.Engine(rt).ReadSession(sa, []byte(s.s.Output), assets.IgnoreMissing)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal session: %w", err)
	}

	// walk through our session, populate seen runs
	s.seenRuns = make(map[flows.RunUUID]time.Time, len(session.Runs()))
	for _, r := range session.Runs() {
		s.seenRuns[r.UUID()] = r.ModifiedOn()
	}

	return session, nil
}

func (s *Session) calculateFires(oa *OrgAssets, sprint flows.Sprint, initial bool) []*ContactFire {
	waitExpiresOn, waitTimeout, queuesToCourier := getWaitProperties(oa, sprint.Events())
	var waitTimeoutOn *time.Time
	if waitTimeout != nil {
		if queuesToCourier {
			s.timeout = waitTimeout
		} else {
			ton := dates.Now().Add(*waitTimeout)
			waitTimeoutOn = &ton
		}
	}

	fs := make([]*ContactFire, 0, 3)

	if waitTimeoutOn != nil {
		fs = append(fs, newContactFireForSession(oa.OrgID(), s, ContactFireTypeWaitTimeout, *waitTimeoutOn))
	}
	if waitExpiresOn != nil {
		fs = append(fs, newContactFireForSession(oa.OrgID(), s, ContactFireTypeWaitExpiration, *waitExpiresOn))
	}
	if initial && s.Status() == SessionStatusWaiting {
		// session expiration time is the creation time + 30 days + random time between 0 and 24 hours
		sessionExpiresOn := s.CreatedOn().Add(SessionExpires).Add(time.Duration(random.IntN(86_400)) * time.Second)

		fs = append(fs, newContactFireForSession(oa.OrgID(), s, ContactFireTypeSessionExpiration, sessionExpiresOn))
	}

	return fs
}

// looks thru sprint events to figure out if we have a wait on this session and if so what is its expiration and timeout
func getWaitProperties(oa *OrgAssets, evts []flows.Event) (*time.Time, *time.Duration, bool) {
	var expiresOn *time.Time
	var timeout *time.Duration
	var queuesToCourier bool

	for _, e := range evts {
		switch typed := e.(type) {
		case *events.MsgWaitEvent:
			expiresOn = &typed.ExpiresOn

			if typed.TimeoutSeconds != nil {
				t := time.Duration(*typed.TimeoutSeconds) * time.Second
				timeout = &t
			}
		case *events.DialWaitEvent:
			expiresOn = &typed.ExpiresOn
		case *events.MsgCreatedEvent:
			if typed.Msg.Channel() != nil {
				channel := oa.ChannelByUUID(typed.Msg.Channel().UUID)
				if channel != nil && !channel.IsAndroid() {
					queuesToCourier = true
				}
			}
		}
	}

	return expiresOn, timeout, queuesToCourier
}

const sqlUpdateSession = `
UPDATE 
	flows_flowsession
SET 
	output = :output, 
	output_url = :output_url,
	status = :status,
	last_sprint_uuid = :last_sprint_uuid,
	ended_on = :ended_on,
	current_flow_id = :current_flow_id
WHERE 
	uuid = :uuid`

const sqlUpdateSessionNoOutput = `
UPDATE 
	flows_flowsession
SET 
	output_url = :output_url,
	status = :status, 
	last_sprint_uuid = :last_sprint_uuid,
	ended_on = :ended_on,
	current_flow_id = :current_flow_id
WHERE 
	uuid = :uuid`

// Update updates the session based on the state passed in from our engine session, this also takes care of applying any event hooks
func (s *Session) Update(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, fs flows.Session, sprint flows.Sprint, contact *Contact, hook SessionCommitHook) error {
	// make sure we have our seen runs
	if s.seenRuns == nil {
		return fmt.Errorf("missing seen runs, cannot update session")
	}

	output, err := json.Marshal(fs)
	if err != nil {
		return fmt.Errorf("error marshalling flow session: %w", err)
	}
	s.s.Output = null.String(output)

	// map our status over
	status, found := sessionStatusMap[fs.Status()]
	if !found {
		return fmt.Errorf("unknown session status: %s", fs.Status())
	}
	s.s.Status = status
	s.s.LastSprintUUID = null.String(sprint.UUID())

	if s.s.Status != SessionStatusWaiting {
		now := time.Now()
		s.s.EndedOn = &now
	}

	// now build up our runs
	for _, r := range fs.Runs() {
		run, err := newRun(ctx, tx, oa, s, r)
		if err != nil {
			return fmt.Errorf("error creating run: %s: %w", r.UUID(), err)
		}

		// set the run on our session
		s.runs = append(s.runs, run)
	}

	// set our sprint
	s.sprint = sprint
	s.s.CurrentFlowID = NilFlowID

	// run through our runs to figure out our current flow
	for _, r := range fs.Runs() {
		// if this run is waiting, save it as the current flow
		if r.Status() == flows.RunStatusWaiting {
			flowID, err := FlowIDForUUID(ctx, tx, oa, r.FlowReference().UUID)
			if err != nil {
				return fmt.Errorf("error loading flow: %s: %w", r.FlowReference().UUID, err)
			}
			s.s.CurrentFlowID = flowID
		}
	}

	// the SQL statement we'll use to update this session
	updateSQL := sqlUpdateSession

	// if writing to S3, do so
	if rt.Config.SessionStorage == "s3" {
		if err := writeSessionsToStorage(ctx, rt, oa.OrgID(), []*Session{s}, []*Contact{contact}); err != nil {
			slog.Error("error writing session to s3", "error", err)
		}

		// don't write output in our SQL
		updateSQL = sqlUpdateSessionNoOutput
	}

	// write our new session state to the db
	if _, err := tx.NamedExecContext(ctx, updateSQL, s.s); err != nil {
		return fmt.Errorf("error updating session: %w", err)
	}

	// clear and recreate any wait expires/timeout fires
	if _, err := DeleteSessionContactFires(ctx, tx, []ContactID{s.ContactID()}, s.Status() != SessionStatusWaiting); err != nil {
		return fmt.Errorf("error deleting session contact fires: %w", err)
	}

	fires := s.calculateFires(oa, sprint, false)

	if err := InsertContactFires(ctx, tx, fires); err != nil {
		return fmt.Errorf("error inserting session contact fires: %w", err)
	}

	// figure out which runs are new and which are updated
	updatedRuns := make([]*FlowRun, 0, 1)
	newRuns := make([]*FlowRun, 0)

	for _, r := range s.Runs() {
		modified, found := s.seenRuns[r.UUID]
		if !found {
			newRuns = append(newRuns, r)
			continue
		}

		if r.ModifiedOn.After(modified) {
			updatedRuns = append(updatedRuns, r)
			continue
		}
	}

	// call our global pre commit hook if present
	if hook != nil {
		err := hook(ctx, tx, rt.RP, oa, []*Session{s})
		if err != nil {
			return fmt.Errorf("error calling commit hook: %v: %w", hook, err)
		}
	}

	// update all modified runs at once
	if err := UpdateRuns(ctx, tx, updatedRuns); err != nil {
		return fmt.Errorf("error updating existing runs: %w", err)
	}

	// insert all new runs at once
	if err := InsertRuns(ctx, tx, newRuns); err != nil {
		return fmt.Errorf("error inserting new runs: %w", err)
	}

	if err := RecordFlowStatistics(ctx, rt, tx, []flows.Session{fs}, []flows.Sprint{sprint}); err != nil {
		return fmt.Errorf("error saving flow statistics: %w", err)
	}

	return nil
}

// MarshalJSON is our custom marshaller so that our inner struct get output
func (s *Session) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.s)
}

// UnmarshalJSON is our custom marshaller so that our inner struct get output
func (s *Session) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &s.s)
}

// NewSession a session objects from the passed in flow session. It does NOT
// commit said session to the database.
func NewSession(ctx context.Context, tx *sqlx.Tx, oa *OrgAssets, fs flows.Session, sprint flows.Sprint, startID StartID, callID CallID) (*Session, error) {
	output, err := json.Marshal(fs)
	if err != nil {
		return nil, fmt.Errorf("error marshalling flow session: %w", err)
	}

	// map our status over
	sessionStatus, found := sessionStatusMap[fs.Status()]
	if !found {
		return nil, fmt.Errorf("unknown session status: %s", fs.Status())
	}

	// session must have at least one run
	if len(fs.Runs()) < 1 {
		return nil, fmt.Errorf("cannot write session that has no runs")
	}

	// figure out our type
	sessionType, found := flowTypeMapping[fs.Type()]
	if !found {
		return nil, fmt.Errorf("unknown flow type: %s", fs.Type())
	}

	// create our session object
	session := &Session{}
	s := &session.s
	s.UUID = fs.UUID()
	s.Status = sessionStatus
	s.LastSprintUUID = null.String(sprint.UUID())
	s.SessionType = sessionType
	s.Output = null.String(output)
	s.ContactID = ContactID(fs.Contact().ID())
	s.CallID = callID
	s.CreatedOn = fs.Runs()[0].CreatedOn()

	if s.Status != SessionStatusWaiting {
		now := time.Now()
		s.EndedOn = &now
	}

	session.sprint = sprint

	// now build up our runs
	for i, r := range fs.Runs() {
		run, err := newRun(ctx, tx, oa, session, r)
		if err != nil {
			return nil, fmt.Errorf("error creating run: %s: %w", r.UUID(), err)
		}

		// set start id if first run of session
		if i == 0 && startID != NilStartID {
			run.StartID = startID
		}

		// save the run to our session
		session.runs = append(session.runs, run)

		// if this run is waiting, save it as the current flow
		if r.Status() == flows.RunStatusWaiting {
			flowID, err := FlowIDForUUID(ctx, tx, oa, r.FlowReference().UUID)
			if err != nil {
				return nil, fmt.Errorf("error loading current flow for UUID: %s: %w", r.FlowReference().UUID, err)
			}
			s.CurrentFlowID = flowID
		}
	}

	return session, nil
}

const sqlInsertWaitingSession = `
INSERT INTO
	flows_flowsession( uuid,  session_type,  status,  last_sprint_uuid,  output,  output_url,  contact_id,  created_on,  current_flow_id,  call_id)
               VALUES(:uuid, :session_type, :status, :last_sprint_uuid, :output, :output_url, :contact_id, :created_on, :current_flow_id, :call_id)`

const sqlInsertWaitingSessionNoOutput = `
INSERT INTO
	flows_flowsession( uuid,  session_type,  status,  last_sprint_uuid,  output_url,  contact_id,  created_on,  current_flow_id,  call_id)
               VALUES(:uuid, :session_type, :status, :last_sprint_uuid, :output_url, :contact_id, :created_on, :current_flow_id, :call_id)`

const sqlInsertEndedSession = `
INSERT INTO
	flows_flowsession( uuid,  session_type,  status,  last_sprint_uuid,  output,  output_url,  contact_id,  created_on,  ended_on,  call_id)
               VALUES(:uuid, :session_type, :status, :last_sprint_uuid, :output, :output_url, :contact_id, :created_on, :ended_on, :call_id)`

const sqlInsertEndedSessionNoOutput = `
INSERT INTO
	flows_flowsession( uuid,  session_type,  status,  last_sprint_uuid,  output_url,  contact_id,  created_on,  ended_on,  call_id)
               VALUES(:uuid, :session_type, :status, :last_sprint_uuid, :output_url, :contact_id, :created_on, :ended_on, :call_id)`

// InsertSessions writes the passed in session to our database, writes any runs that need to be created
// as well as appying any events created in the session
func InsertSessions(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, ss []flows.Session, sprints []flows.Sprint, contacts []*Contact, hook SessionCommitHook, startID StartID, callID CallID) ([]*Session, error) {
	if len(ss) == 0 {
		return nil, nil
	}

	// create all our session objects
	sessions := make([]*Session, 0, len(ss))
	runs := make([]*FlowRun, 0, len(sessions))
	waitingSessionsI := make([]any, 0, len(ss))
	endedSessionsI := make([]any, 0, len(ss))
	fires := make([]*ContactFire, 0, len(ss))
	waitingContactIDs := make([]ContactID, 0, len(ss))

	for i, s := range ss {
		session, err := NewSession(ctx, tx, oa, s, sprints[i], startID, callID)
		if err != nil {
			return nil, fmt.Errorf("error creating session objects: %w", err)
		}
		sessions = append(sessions, session)
		runs = append(runs, session.runs...)
		fires = append(fires, session.calculateFires(oa, sprints[i], true)...)

		if session.Status() == SessionStatusWaiting {
			waitingSessionsI = append(waitingSessionsI, &session.s)
			waitingContactIDs = append(waitingContactIDs, session.s.ContactID)
		} else {
			endedSessionsI = append(endedSessionsI, &session.s)
		}
	}

	// call our global pre commit hook if present
	if hook != nil {
		err := hook(ctx, tx, rt.RP, oa, sessions)
		if err != nil {
			return nil, fmt.Errorf("error calling commit hook: %v: %w", hook, err)
		}
	}

	// the SQL we'll use to do our insert of sessions
	insertEndedSQL := sqlInsertEndedSession
	insertWaitingSQL := sqlInsertWaitingSession

	// if writing our sessions to S3, do so
	if rt.Config.SessionStorage == "s3" {
		err := writeSessionsToStorage(ctx, rt, oa.OrgID(), sessions, contacts)
		if err != nil {
			return nil, fmt.Errorf("error writing sessions to storage: %w", err)
		}

		insertEndedSQL = sqlInsertEndedSessionNoOutput
		insertWaitingSQL = sqlInsertWaitingSessionNoOutput
	}

	// insert our ended sessions first
	err := BulkQuery(ctx, "insert ended sessions", tx, insertEndedSQL, endedSessionsI)
	if err != nil {
		return nil, fmt.Errorf("error inserting ended sessions: %w", err)
	}

	// insert waiting sessions
	err = BulkQuery(ctx, "insert waiting sessions", tx, insertWaitingSQL, waitingSessionsI)
	if err != nil {
		return nil, fmt.Errorf("error inserting waiting sessions: %w", err)
	}

	// insert all runs
	err = BulkQuery(ctx, "insert runs", tx, sqlInsertRun, runs)
	if err != nil {
		return nil, fmt.Errorf("error writing runs: %w", err)
	}

	numFiresDeleted, err := DeleteSessionContactFires(ctx, tx, waitingContactIDs, true)
	if err != nil {
		return nil, fmt.Errorf("error deleting session contact fires: %w", err)
	}
	if numFiresDeleted > 0 {
		slog.With("org_id", oa.OrgID()).Error("deleted session contact fires that shouldn't have been there", "count", numFiresDeleted)
	}

	// insert all our contact fires
	if err := InsertContactFires(ctx, tx, fires); err != nil {
		return nil, fmt.Errorf("error inserting session contact fires: %w", err)
	}

	if err := RecordFlowStatistics(ctx, rt, tx, ss, sprints); err != nil {
		return nil, fmt.Errorf("error saving flow statistics: %w", err)
	}

	// return our session
	return sessions, nil
}

const sqlSelectSessionByUUID = `
SELECT uuid, session_type, status, last_sprint_uuid, output, output_url, contact_id, created_on, ended_on, current_flow_id, call_id
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
	if session.s.Status != SessionStatusWaiting || session.s.ContactID != ContactID(fc.ID()) {
		slog.Error("current session for contact isn't a waiting session", "session_uuid", uuid, "contact_id", fc.ID())
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
      SET status = $2, ended_on = NOW(), current_flow_id = NULL
    WHERE uuid = ANY($1) AND status = 'W'
RETURNING contact_id`

// TODO instead of having an index on session_uuid.. rework this to fetch the sessions and extract a list of run uuids?
const sqlExitSessionRuns = `
UPDATE flows_flowrun
   SET exited_on = NOW(), status = $2, modified_on = NOW()
 WHERE session_uuid = ANY($1) AND status IN ('A', 'W')`

const sqlExitSessionContacts = `
 UPDATE contacts_contact 
    SET current_session_uuid = NULL, current_flow_id = NULL, modified_on = NOW() 
  WHERE id = ANY($1) AND current_session_uuid = ANY($2)`

// exits sessions and their runs inside the given transaction
func exitSessionBatch(ctx context.Context, tx *sqlx.Tx, uuids []flows.SessionUUID, status SessionStatus) error {
	runStatus := RunStatus(status) // session status codes are subset of run status codes
	contactIDs := make([]ContactID, 0, len(uuids))

	// first update the sessions themselves and get the contact ids
	if err := tx.SelectContext(ctx, &contactIDs, sqlExitSessions, pq.Array(uuids), status); err != nil {
		return fmt.Errorf("error exiting sessions: %w", err)
	}

	// then the runs that belong to these sessions
	if _, err := tx.ExecContext(ctx, sqlExitSessionRuns, pq.Array(uuids), runStatus); err != nil {
		return fmt.Errorf("error exiting session runs: %w", err)
	}

	// and finally the contacts from each session
	if _, err := tx.ExecContext(ctx, sqlExitSessionContacts, pq.Array(contactIDs), pq.Array(uuids)); err != nil {
		return fmt.Errorf("error exiting sessions: %w", err)
	}

	// delete any session related fires for these contacts
	if _, err := DeleteSessionContactFires(ctx, tx, contactIDs, true); err != nil {
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
