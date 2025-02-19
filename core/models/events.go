package models

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
)

// Scene represents the context that events are occurring in
type Scene struct {
	contact *flows.Contact
	session *Session
	userID  UserID

	preCommits  map[EventCommitHook][]any
	postCommits map[EventCommitHook][]any
}

// NewSceneForSession creates a new scene for the passed in session
func NewSceneForSession(session *Session) *Scene {
	return &Scene{
		contact: session.Contact(),
		session: session,

		preCommits:  make(map[EventCommitHook][]any),
		postCommits: make(map[EventCommitHook][]any),
	}
}

// NewSceneForContact creates a new scene for the passed in contact, session will be nil
func NewSceneForContact(contact *flows.Contact, userID UserID) *Scene {
	return &Scene{
		contact: contact,
		userID:  userID,

		preCommits:  make(map[EventCommitHook][]any),
		postCommits: make(map[EventCommitHook][]any),
	}
}

// SessionID returns the session id for this scene if any
func (s *Scene) SessionID() SessionID {
	if s.session == nil {
		return SessionID(0)
	}
	return s.session.ID()
}

func (s *Scene) Contact() *flows.Contact        { return s.contact }
func (s *Scene) ContactID() ContactID           { return ContactID(s.contact.ID()) }
func (s *Scene) ContactUUID() flows.ContactUUID { return s.contact.UUID() }

// Session returns the session for this scene if any
func (s *Scene) Session() *Session { return s.session }

// User returns the user ID for this scene if any
func (s *Scene) UserID() UserID { return s.userID }

// AppendToEventPreCommitHook adds a new event to be handled by a pre commit hook
func (s *Scene) AppendToEventPreCommitHook(hook EventCommitHook, event any) {
	s.preCommits[hook] = append(s.preCommits[hook], event)
}

// AppendToEventPostCommitHook adds a new event to be handled by a post commit hook
func (s *Scene) AppendToEventPostCommitHook(hook EventCommitHook, event any) {
	s.postCommits[hook] = append(s.postCommits[hook], event)
}

// EventHandler defines a call for handling events that occur in a flow
type EventHandler func(context.Context, *runtime.Runtime, *sqlx.Tx, *OrgAssets, *Scene, flows.Event) error

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

// HandleEvents handles the passed in event, IE, creates the db objects required etc..
func HandleEvents(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, scene *Scene, events []flows.Event) error {
	for _, e := range events {

		handler, found := eventHandlers[e.Type()]
		if !found {
			return fmt.Errorf("unable to find handler for event type: %s", e.Type())
		}

		err := handler(ctx, rt, tx, oa, scene, e)
		if err != nil {
			return err
		}
	}
	return nil
}

// EventCommitHook defines a callback that will accept a certain type of events across session, either before or after committing
type EventCommitHook interface {
	Apply(context.Context, *runtime.Runtime, *sqlx.Tx, *OrgAssets, map[*Scene][]any) error
}

// ApplyEventPreCommitHooks runs through all the pre event hooks for the passed in sessions and applies their events
func ApplyEventPreCommitHooks(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, scenes []*Scene) error {
	// gather all our hook events together across our sessions
	preHooks := make(map[EventCommitHook]map[*Scene][]any)
	for _, s := range scenes {
		for hook, args := range s.preCommits {
			sessionMap, found := preHooks[hook]
			if !found {
				sessionMap = make(map[*Scene][]any, len(scenes))
				preHooks[hook] = sessionMap
			}
			sessionMap[s] = args
		}
	}

	// now fire each of our hooks
	for hook, args := range preHooks {
		err := hook.Apply(ctx, rt, tx, oa, args)
		if err != nil {
			return fmt.Errorf("error applying events pre commit hook: %T: %w", hook, err)
		}
	}

	return nil
}

// ApplyEventPostCommitHooks runs through all the post event hooks for the passed in sessions and applies their events
func ApplyEventPostCommitHooks(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *OrgAssets, scenes []*Scene) error {
	// gather all our hook events together across our sessions
	postHooks := make(map[EventCommitHook]map[*Scene][]any)
	for _, s := range scenes {
		for hook, args := range s.postCommits {
			sprintMap, found := postHooks[hook]
			if !found {
				sprintMap = make(map[*Scene][]any, len(scenes))
				postHooks[hook] = sprintMap
			}
			sprintMap[s] = args
		}
	}

	// now fire each of our hooks
	for hook, args := range postHooks {
		err := hook.Apply(ctx, rt, tx, oa, args)
		if err != nil {
			return fmt.Errorf("error applying post commit hook: %v: %w", hook, err)
		}
	}

	return nil
}

// HandleAndCommitEvents takes a set of contacts and events, handles the events and applies any hooks, and commits everything
func HandleAndCommitEvents(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, userID UserID, contactEvents map[*flows.Contact][]flows.Event) error {
	// create scenes for each contact
	scenes := make([]*Scene, 0, len(contactEvents))
	for contact := range contactEvents {
		scene := NewSceneForContact(contact, userID)
		scenes = append(scenes, scene)
	}

	// begin the transaction for pre-commit hooks
	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	// handle the events to create the hooks on each scene
	for _, scene := range scenes {
		err := HandleEvents(ctx, rt, tx, oa, scene, contactEvents[scene.Contact()])
		if err != nil {
			return fmt.Errorf("error applying events: %w", err)
		}
	}

	// gather all our pre commit events, group them by hook and apply them
	err = ApplyEventPreCommitHooks(ctx, rt, tx, oa, scenes)
	if err != nil {
		return fmt.Errorf("error applying pre commit hooks: %w", err)
	}

	// commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing pre commit hooks: %w", err)
	}

	// begin the transaction for post-commit hooks
	tx, err = rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction for post commit: %w", err)
	}

	// apply the post commit hooks
	err = ApplyEventPostCommitHooks(ctx, rt, tx, oa, scenes)
	if err != nil {
		return fmt.Errorf("error applying post commit hooks: %w", err)
	}

	// commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing post commit hooks: %w", err)
	}
	return nil
}

// ApplyModifiers modifies contacts by applying modifiers and handling the resultant events
// Note that we don't load the user object from org assets because it's possible that the user isn't part
// of the org, e.g. customer support.
func ApplyModifiers(ctx context.Context, rt *runtime.Runtime, oa *OrgAssets, userID UserID, modifiersByContact map[*flows.Contact][]flows.Modifier) (map[*flows.Contact][]flows.Event, error) {
	// create an environment instance with location support
	env := flows.NewAssetsEnvironment(oa.Env(), oa.SessionAssets().Locations())

	eng := goflow.Engine(rt)

	eventsByContact := make(map[*flows.Contact][]flows.Event, len(modifiersByContact))

	// apply the modifiers to get the events for each contact
	for contact, mods := range modifiersByContact {
		events := make([]flows.Event, 0)
		for _, mod := range mods {
			modifiers.Apply(eng, env, oa.SessionAssets(), contact, mod, func(e flows.Event) { events = append(events, e) })
		}
		eventsByContact[contact] = events
	}

	err := HandleAndCommitEvents(ctx, rt, oa, userID, eventsByContact)
	if err != nil {
		return nil, fmt.Errorf("error commiting events: %w", err)
	}

	return eventsByContact, nil
}

// TypeSprintEnded is a pseudo event that lets add hooks for changes to a contacts current flow or flow history
const TypeSprintEnded string = "sprint_ended"

type SprintEndedEvent struct {
	events.BaseEvent

	Contact *Contact // model contact so we can access current flow
	Resumed bool     // whether this was a resume
}

// NewSprintEndedEvent creates a new sprint ended event
func NewSprintEndedEvent(c *Contact, resumed bool) *SprintEndedEvent {
	return &SprintEndedEvent{
		BaseEvent: events.NewBaseEvent(TypeSprintEnded),
		Contact:   c,
		Resumed:   resumed,
	}
}
