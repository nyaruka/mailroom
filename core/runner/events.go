package runner

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// EventHandler defines a call for handling events that occur in a flow
type EventHandler func(context.Context, *runtime.Runtime, *models.OrgAssets, *Scene, flows.Event, models.UserID) error

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

// TypeSessionInterrupted is a pseudo event to interrupt a session
const TypeSessionInterrupted string = "session_interrupted"

type SessionInterruptedEvent struct {
	events.BaseEvent

	SessionUUID flows.SessionUUID
}

func newSessionInterruptedEvent(sessionUUID flows.SessionUUID) *SessionInterruptedEvent {
	return &SessionInterruptedEvent{
		BaseEvent:   events.NewBaseEvent(TypeSessionInterrupted),
		SessionUUID: sessionUUID,
	}
}
