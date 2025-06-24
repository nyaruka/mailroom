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
