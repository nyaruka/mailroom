package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// ResumeFlow resumes the passed in session using the passed in session
func ResumeFlow(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, session *models.Session, contact *models.Contact, resume flows.Resume, sceneInit func(*Scene)) (*models.Session, error) {
	start := time.Now()
	sa := oa.SessionAssets()

	// does the flow this session is part of still exist?
	_, err := oa.FlowByID(session.CurrentFlowID())
	if err != nil {
		// if this flow just isn't available anymore, log this error
		if err == models.ErrNotFound {
			slog.Error("unable to find flow for resume", "contact", contact.UUID(), "session", session.UUID(), "flow_id", session.CurrentFlowID())

			return nil, models.ExitSessions(ctx, rt.DB, []flows.SessionUUID{session.UUID()}, models.SessionStatusFailed)
		}
		return nil, fmt.Errorf("error loading session flow: %d: %w", session.CurrentFlowID(), err)
	}

	// build our flow session
	fs, err := session.FlowSession(ctx, rt, sa, oa.Env())
	if err != nil {
		return nil, fmt.Errorf("unable to create session from output: %w", err)
	}

	// resume our session
	sprint, err := fs.Resume(ctx, resume)

	// had a problem resuming our flow? bail
	if err != nil {
		return nil, fmt.Errorf("error resuming flow: %w", err)
	}

	scene := NewSessionScene(fs, sprint, sceneInit)

	var eventsToHandle []flows.Event

	// if session didn't fail, we also need to include changes from sprint events
	if fs.Status() != flows.SessionStatusFailed {
		eventsToHandle = append(eventsToHandle, sprint.Events()...)
	}

	eventsToHandle = append(eventsToHandle, newSprintEndedEvent(contact, true))

	if err := scene.AddEvents(ctx, rt, oa, eventsToHandle); err != nil {
		return nil, fmt.Errorf("error handling events for session %s: %w", session.UUID(), err)
	}

	// write our updated session, applying any events in the process
	txCTX, cancel := context.WithTimeout(ctx, commitTimeout)
	defer cancel()

	tx, err := rt.DB.BeginTxx(txCTX, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}

	// write our updated session and runs
	timeout, err := session.Update(txCTX, rt, tx, oa, fs, sprint, contact)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error updating session for resume: %w", err)
	}
	scene.WaitTimeout = timeout

	if err := ExecutePreCommitHooks(ctx, rt, tx, oa, []*Scene{scene}); err != nil {
		return nil, fmt.Errorf("error applying pre commit hooks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error committing resumption of flow: %w", err)
	}

	// now take care of any post-commit hooks
	if err := ExecutePostCommitHooks(ctx, rt, oa, []*Scene{scene}); err != nil {
		return nil, fmt.Errorf("error processing post commit hooks: %w", err)
	}

	slog.Debug("resumed session", "contact", resume.Contact().UUID(), "session", session.UUID(), "resume_type", resume.Type(), "elapsed", time.Since(start))

	return session, nil
}
