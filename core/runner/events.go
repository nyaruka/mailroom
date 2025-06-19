package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// EventHandler defines a call for handling events that occur in a flow
type EventHandler func(context.Context, *runtime.Runtime, *models.OrgAssets, *Scene, flows.Event) error

// our registry of event type to internal handlers
var eventHandlers = make(map[string]EventHandler)

// RegisterEventHandler registers the passed in handler as being interested in the passed in type
func RegisterEventHandler(eventType string, handler EventHandler) {
	// it's a bug if we try to register more than one handler for a type
	_, found := eventHandlers[eventType]
	if found {
		panic(fmt.Errorf("duplicate handler being registered for type: %s", eventType))
	}
	eventHandlers[eventType] = handler
}

// TypeSprintEnded is a pseudo event that lets add hooks for changes to a contacts current flow or flow history
const TypeSprintEnded string = "sprint_ended"

type SprintEndedEvent struct {
	events.BaseEvent

	Contact *models.Contact // model contact so we can access current flow
	Resumed bool            // whether this was a resume
}

// creates a new sprint ended event
func newSprintEndedEvent(c *models.Contact, resumed bool) *SprintEndedEvent {
	return &SprintEndedEvent{
		BaseEvent: events.NewBaseEvent(TypeSprintEnded),
		Contact:   c,
		Resumed:   resumed,
	}
}

// Scene represents the context that events are occurring in
type Scene struct {
	contact *flows.Contact
	session flows.Session
	sprint  flows.Sprint
	userID  models.UserID

	Call        *models.Call
	IncomingMsg *models.MsgInRef
	WaitTimeout time.Duration

	events      []flows.Event
	preCommits  map[PreCommitHook][]any
	postCommits map[PostCommitHook][]any
}

// NewScene creates a new scene for the passed in contact
func NewScene(contact *flows.Contact, userID models.UserID, init func(*Scene)) *Scene {
	s := &Scene{
		contact: contact,
		userID:  userID,

		events:      make([]flows.Event, 0, 10),
		preCommits:  make(map[PreCommitHook][]any),
		postCommits: make(map[PostCommitHook][]any),
	}
	if init != nil {
		init(s)
	}
	return s
}

func (s *Scene) Contact() *flows.Contact        { return s.contact }
func (s *Scene) ContactID() models.ContactID    { return models.ContactID(s.contact.ID()) }
func (s *Scene) ContactUUID() flows.ContactUUID { return s.contact.UUID() }
func (s *Scene) Session() flows.Session         { return s.session }
func (s *Scene) Sprint() flows.Sprint           { return s.sprint }
func (s *Scene) UserID() models.UserID          { return s.userID }

// SessionUUID is a convenience utility to get the session UUID for this scene if any
func (s *Scene) SessionUUID() flows.SessionUUID {
	if s.session == nil {
		return ""
	}
	return s.session.UUID()
}

// SprintUUID is a convenience utility to get the sprint UUID for this scene if any
func (s *Scene) SprintUUID() flows.SprintUUID {
	if s.sprint == nil {
		return ""
	}
	return s.sprint.UUID()
}

// LocateEvent finds the flow and node UUID for an event belonging to this session
func (s *Scene) LocateEvent(e flows.Event) (*models.Flow, flows.NodeUUID) {
	run, step := s.session.FindStep(e.StepUUID())
	flow := run.Flow().Asset().(*models.Flow)
	return flow, step.NodeUUID()
}

func (s *Scene) AddEvents(evts []flows.Event) {
	s.events = append(s.events, evts...)
}

func (s *Scene) AddSprint(ss flows.Session, sp flows.Sprint, mc *models.Contact, resumed bool) {
	s.session = ss
	s.sprint = sp

	// if session didn't fail, accept it's state changes
	if ss.Status() != flows.SessionStatusFailed {
		s.contact = ss.Contact() // update contact

		s.AddEvents(sp.Events())
	}

	s.AddEvents([]flows.Event{newSprintEndedEvent(mc, resumed)})
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

// ProcessEvents allows processing of events generated outside of a flow
func ProcessEvents(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, userID models.UserID, contactEvents map[*flows.Contact][]flows.Event, sceneInit func(*Scene)) error {
	// create scenes for each contact
	scenes := make([]*Scene, 0, len(contactEvents))
	for contact := range contactEvents {
		scene := NewScene(contact, userID, sceneInit)
		scene.AddEvents(contactEvents[contact])
		scenes = append(scenes, scene)
	}

	// begin the transaction for pre-commit hooks
	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	// handle the events to create the hooks on each scene
	for _, scene := range scenes {
		if err := scene.ProcessEvents(ctx, rt, oa); err != nil {
			return fmt.Errorf("error applying events: %w", err)
		}
	}

	// gather all our pre commit events, group them by hook and apply them
	if err := ExecutePreCommitHooks(ctx, rt, tx, oa, scenes); err != nil {
		return fmt.Errorf("error applying pre commit hooks: %w", err)
	}

	// commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing pre commit hooks: %w", err)
	}

	// now take care of any post-commit hooks
	if err := ExecutePostCommitHooks(ctx, rt, oa, scenes); err != nil {
		return fmt.Errorf("error processing post commit hooks: %w", err)
	}

	return nil
}
