package runner

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// EventHandler defines a call for handling events that occur in a flow
type EventHandler func(context.Context, *runtime.Runtime, *models.OrgAssets, *Scene, events.Event, models.UserID) error

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

// WrappedEventHandler is like EventHandler but receives the handler it wrapped as its final
// argument so it can delegate to it.
type WrappedEventHandler func(context.Context, *runtime.Runtime, *models.OrgAssets, *Scene, events.Event, models.UserID, EventHandler) error

// WrapEventHandler replaces the handler for the given event type with the given handler, which
// receives the replaced handler as its final argument. This allows extensions to add their own
// handling of an event type whilst deferring to the existing handler.
func WrapEventHandler(eventType string, handler WrappedEventHandler) {
	base := eventHandlers[eventType]
	if base == nil {
		panic(fmt.Errorf("no handler registered for type %s to wrap", eventType))
	}
	eventHandlers[eventType] = func(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, s *Scene, e events.Event, userID models.UserID) error {
		return handler(ctx, rt, oa, s, e, userID, base)
	}
}

// TypeContactCreated is a pseudo event raised by callers that create contacts (e.g. imports) so that handlers can
// add hooks for newly created contacts - which might not otherwise generate any events.
const TypeContactCreated string = "contact_created"

type ContactCreatedEvent struct {
	events.BaseEvent
}

// NewContactCreatedEvent creates a new contact created event
func NewContactCreatedEvent() *ContactCreatedEvent {
	return &ContactCreatedEvent{
		BaseEvent: events.NewBaseEvent(TypeContactCreated),
	}
}

// TypeContactInterrupted is a pseudo event that lets add hooks for session interruption
const TypeContactInterrupted string = "contact_interrupted"

type ContactInterruptedEvent struct {
	events.BaseEvent

	Status flows.SessionStatus
}

func newContactInterruptedEvent(status flows.SessionStatus) *ContactInterruptedEvent {
	return &ContactInterruptedEvent{
		BaseEvent: events.NewBaseEvent(TypeContactInterrupted),
		Status:    status,
	}
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
