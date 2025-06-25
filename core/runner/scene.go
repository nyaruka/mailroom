package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
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
	UserID      models.UserID
	StartID     models.StartID
	IncomingMsg *models.MsgInRef
	Interrupt   bool

	// optional state set during processing
	Session     flows.Session
	Sprint      flows.Sprint
	WaitTimeout time.Duration

	events      []flows.Event
	preCommits  map[PreCommitHook][]any
	postCommits map[PostCommitHook][]any
}

// NewScene creates a new scene for the passed in contact
func NewScene(dbContact *models.Contact, contact *flows.Contact, userID models.UserID) *Scene {
	return &Scene{
		DBContact: dbContact,
		Contact:   contact,
		UserID:    userID,

		events:      make([]flows.Event, 0, 10),
		preCommits:  make(map[PreCommitHook][]any),
		postCommits: make(map[PostCommitHook][]any),
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

func (s *Scene) AddEvents(evts []flows.Event) {
	s.events = append(s.events, evts...)
}

func (s *Scene) AddSprint(ss flows.Session, sp flows.Sprint, resumed bool) {
	s.Session = ss
	s.Sprint = sp

	// if session didn't fail, accept it's state changes
	if ss.Status() != flows.SessionStatusFailed {
		s.Contact = ss.Contact() // update contact

		s.AddEvents(sp.Events())
	}

	s.AddEvents([]flows.Event{newSprintEndedEvent(s.DBContact, resumed)})
}

// ProcessEvents runs this scene's events through the appropriate handlers which in turn attach hooks to the scene
func (s *Scene) ProcessEvents(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	for _, e := range s.events {
		handler, found := eventHandlers[e.Type()]
		if !found {
			return fmt.Errorf("unable to find handler for event type: %s", e.Type())
		}

		if err := handler(ctx, rt, oa, s, e); err != nil {
			return err
		}
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
