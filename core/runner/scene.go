package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// Scene represents the context that events are occurring in
type Scene struct {
	// required state set on creation
	DBContact *models.Contact
	Contact   *flows.Contact

	// optional state set on creation
	DBCall      *models.Call
	Call        *flows.Call
	StartID     models.StartID
	IncomingMsg *models.MsgInRef
	Interrupt   bool

	// optional state set during processing
	DBSession           *models.Session
	Session             flows.Session
	Sprint              flows.Sprint
	WaitTimeout         time.Duration
	PriorRunModifiedOns map[flows.RunUUID]time.Time

	preCommits  map[PreCommitHook][]any
	postCommits map[PostCommitHook][]any

	// can be overridden by tests
	Engine func(*runtime.Runtime) flows.Engine
}

// NewScene creates a new scene for the passed in contact
func NewScene(dbContact *models.Contact, contact *flows.Contact) *Scene {
	return &Scene{
		DBContact: dbContact,
		Contact:   contact,

		preCommits:  make(map[PreCommitHook][]any),
		postCommits: make(map[PostCommitHook][]any),

		Engine: goflow.Engine,
	}
}

func (s *Scene) ContactID() models.ContactID    { return models.ContactID(s.Contact.ID()) }
func (s *Scene) ContactUUID() flows.ContactUUID { return s.Contact.UUID() }

// SessionUUID is a convenience utility to get the session UUID for this scene if any
func (s *Scene) SessionUUID() flows.SessionUUID {
	if s.Session == nil {
		return ""
	}
	return s.Session.UUID()
}

// SprintUUID is a convenience utility to get the sprint UUID for this scene if any
func (s *Scene) SprintUUID() flows.SprintUUID {
	if s.Sprint == nil {
		return ""
	}
	return s.Sprint.UUID()
}

// LocateEvent finds the flow and node UUID for an event belonging to this session
func (s *Scene) LocateEvent(e flows.Event) (*models.Flow, flows.NodeUUID) {
	run, step := s.Session.FindStep(e.StepUUID())
	flow := run.Flow().Asset().(*models.Flow)
	return flow, step.NodeUUID()
}

func (s *Scene) AddEvent(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, e flows.Event, userID models.UserID) error {
	handler, found := eventHandlers[e.Type()]
	if !found {
		return fmt.Errorf("unable to find handler for event type: %s", e.Type())
	}

	if err := handler(ctx, rt, oa, s, e, userID); err != nil {
		return err
	}

	return nil
}

func (s *Scene) addSprint(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ss flows.Session, sp flows.Sprint, resumed bool) error {
	s.Session = ss
	s.Sprint = sp

	evts := make([]flows.Event, 0, len(sp.Events())+1)

	// if session didn't fail, accept it's state changes
	if ss.Status() != flows.SessionStatusFailed {
		s.Contact = ss.Contact() // update contact

		evts = append(evts, sp.Events()...)
	}

	evts = append(evts, newSprintEndedEvent(s.DBContact, resumed))

	for _, e := range evts {
		if err := s.AddEvent(ctx, rt, oa, e, models.NilUserID); err != nil {
			return fmt.Errorf("error adding event to scene: %w", err)
		}
	}

	return nil
}

// StartSession starts a new session.
func (s *Scene) StartSession(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, trigger flows.Trigger) error {
	session, sprint, err := s.Engine(rt).NewSession(ctx, oa.SessionAssets(), oa.Env(), s.Contact, trigger, s.Call)
	if err != nil {
		return fmt.Errorf("error starting contact %s in flow %s: %w", s.ContactUUID(), trigger.Flow().UUID, err)
	}

	if err := s.addSprint(ctx, rt, oa, session, sprint, false); err != nil {
		return fmt.Errorf("error adding events for session %s: %w", session.UUID(), err)
	}

	return nil
}

// ResumeSession resumes the passed in session
func (s *Scene) ResumeSession(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, session *models.Session, resume flows.Resume) error {
	if s.Sprint != nil {
		panic("scene already has a sprint")
	}

	// does the flow this session is part of still exist?
	_, err := oa.FlowByID(session.CurrentFlowID())
	if err != nil {
		// if this flow just isn't available anymore, log this error
		if err == models.ErrNotFound {
			slog.Error("unable to find flow for resume", "contact", s.ContactUUID(), "session", session.UUID(), "flow_id", session.CurrentFlowID())

			return models.ExitSessions(ctx, rt.DB, []flows.SessionUUID{session.UUID()}, models.SessionStatusFailed)
		}
		return fmt.Errorf("error loading session flow: %d: %w", session.CurrentFlowID(), err)
	}

	// build our flow session
	fs, err := session.EngineSession(ctx, rt, oa.SessionAssets(), oa.Env(), s.Contact, s.Call)
	if err != nil {
		return fmt.Errorf("unable to create session from output: %w", err)
	}

	// record run modified times prior to resuming so we can figure out which runs are new or updated
	s.DBSession = session
	s.PriorRunModifiedOns = make(map[flows.RunUUID]time.Time, len(fs.Runs()))
	for _, r := range fs.Runs() {
		s.PriorRunModifiedOns[r.UUID()] = r.ModifiedOn()
	}

	sprint, err := fs.Resume(ctx, resume)
	if err != nil {
		return fmt.Errorf("error resuming flow: %w", err)
	}

	if err := s.addSprint(ctx, rt, oa, fs, sprint, true); err != nil {
		return fmt.Errorf("error processing events for session %s: %w", session.UUID(), err)
	}

	return nil
}

// AttachPreCommitHook adds an item to be handled by the given pre commit hook
func (s *Scene) AttachPreCommitHook(hook PreCommitHook, item any) {
	s.preCommits[hook] = append(s.preCommits[hook], item)
}

// AttachPostCommitHook adds an item to be handled by the given post commit hook
func (s *Scene) AttachPostCommitHook(hook PostCommitHook, item any) {
	s.postCommits[hook] = append(s.postCommits[hook], item)
}

// Commit commits this scene's events
func (s *Scene) Commit(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	return BulkCommit(ctx, rt, oa, []*Scene{s})
}

// BulkCommit commits the passed in scenes in a single transaction. If that fails, it retries committing each scene one at a time.
func BulkCommit(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes []*Scene) error {
	if len(scenes) == 0 {
		return nil // nothing to do
	}

	txCTX, cancel := context.WithTimeout(ctx, commitTimeout*time.Duration(len(scenes)))
	defer cancel()

	tx, err := rt.DB.BeginTxx(txCTX, nil)
	if err != nil {
		return fmt.Errorf("error starting transaction for bulk scene commit: %w", err)
	}

	if err := ExecutePreCommitHooks(ctx, rt, tx, oa, scenes); err != nil {
		tx.Rollback()
		return fmt.Errorf("error executing scene pre commit hooks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		// retry committing our scenes one at a time
		slog.Debug("failed committing scenes in bulk, retrying one at a time", "error", err)

		tx.Rollback()

		// we failed committing the scenes in one go, try one at a time
		for _, scene := range scenes {
			txCTX, cancel := context.WithTimeout(ctx, commitTimeout)
			defer cancel()

			tx, err := rt.DB.BeginTxx(txCTX, nil)
			if err != nil {
				return fmt.Errorf("error starting transaction for retry: %w", err)
			}

			if err := ExecutePreCommitHooks(ctx, rt, tx, oa, []*Scene{scene}); err != nil {
				return fmt.Errorf("error applying scene pre commit hooks: %w", err)
			}

			if err := tx.Commit(); err != nil {
				tx.Rollback()
				slog.Error("error committing scene", "error", err, "contact", scene.ContactUUID())
				continue
			}
		}
	}

	if err := ExecutePostCommitHooks(ctx, rt, oa, scenes); err != nil {
		return fmt.Errorf("error processing post commit hooks: %w", err)
	}

	return nil
}
