package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

func init() {
	runner.RegisterEventHandler(runner.TypeSprintEnded, handleSprintEnded)
}

func handleSprintEnded(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event) error {
	event := e.(*runner.SprintEndedEvent)

	slog.Debug("sprint ended", "contact", scene.ContactUUID(), "session", scene.SessionUUID())

	currentFlowChanged := false

	// get flow that contact is now waiting in ()
	currentFlowID := models.NilFlowID
	for _, run := range scene.Session().Runs() {
		if run.Status() == flows.RunStatusWaiting {
			currentFlowID = run.Flow().Asset().(*models.Flow).ID()
			break
		}
	}

	// if we're in a flow type that can wait then contact current flow has potentially changed
	if scene.Session().Type() != flows.FlowTypeMessagingBackground {
		var waitingSessionUUID flows.SessionUUID
		if scene.Session().Status() == flows.SessionStatusWaiting {
			waitingSessionUUID = scene.Session().UUID()
		}

		currentFlowChanged = event.Contact.CurrentFlowID() != currentFlowID

		if event.Contact.CurrentSessionUUID() != waitingSessionUUID || currentFlowChanged {
			scene.AttachPreCommitHook(hooks.UpdateContactSession, hooks.CurrentSessionUpdate{
				ID:                 scene.ContactID(),
				CurrentSessionUUID: null.String(waitingSessionUUID),
				CurrentFlowID:      currentFlowID,
			})
		}
	}

	// if current flow has changed then we need to update modified_on, but also if this is a new session
	// then flow history may have changed too in a way that won't be captured by a flow_entered event
	if currentFlowChanged || !event.Resumed {
		scene.AttachPreCommitHook(hooks.UpdateContactModifiedOn, event)
	}

	// if we have a call and the session is no longer waiting, call should be completed
	if scene.Call != nil && scene.Session().Status() != flows.SessionStatusWaiting {
		scene.AttachPreCommitHook(hooks.UpdateCallStatus, models.CallStatusCompleted)
	}

	return nil
}
