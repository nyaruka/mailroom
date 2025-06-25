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

// ResumeSession resumes the passed in session
func ResumeSession(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, session *models.Session, scene *Scene, resume flows.Resume) error {
	start := time.Now()
	sa := oa.SessionAssets()

	// does the flow this session is part of still exist?
	_, err := oa.FlowByID(session.CurrentFlowID())
	if err != nil {
		// if this flow just isn't available anymore, log this error
		if err == models.ErrNotFound {
			slog.Error("unable to find flow for resume", "contact", scene.ContactUUID(), "session", session.UUID(), "flow_id", session.CurrentFlowID())

			return models.ExitSessions(ctx, rt.DB, []flows.SessionUUID{session.UUID()}, models.SessionStatusFailed)
		}
		return fmt.Errorf("error loading session flow: %d: %w", session.CurrentFlowID(), err)
	}

	// build our flow session
	fs, err := session.EngineSession(ctx, rt, sa, oa.Env(), scene.Contact, scene.Call)
	if err != nil {
		return fmt.Errorf("unable to create session from output: %w", err)
	}

	// resume our session
	sprint, err := fs.Resume(ctx, resume)

	// had a problem resuming our flow? bail
	if err != nil {
		return fmt.Errorf("error resuming flow: %w", err)
	}

	scene.AddSprint(fs, sprint, true)

	if err := scene.ProcessEvents(ctx, rt, oa); err != nil {
		return fmt.Errorf("error processing events for session %s: %w", session.UUID(), err)
	}

	// write our updated session, applying any events in the process
	txCTX, cancel := context.WithTimeout(ctx, commitTimeout)
	defer cancel()

	tx, err := rt.DB.BeginTxx(txCTX, nil)
	if err != nil {
		return fmt.Errorf("error starting transaction: %w", err)
	}

	// write our updated session and runs
	if err := session.Update(txCTX, rt, tx, oa, fs, sprint, scene.DBContact); err != nil {
		tx.Rollback()
		return fmt.Errorf("error updating session for resume: %w", err)
	}

	if err := ExecutePreCommitHooks(ctx, rt, tx, oa, []*Scene{scene}); err != nil {
		return fmt.Errorf("error applying pre commit hooks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return fmt.Errorf("error committing resumption of flow: %w", err)
	}

	// now take care of any post-commit hooks
	if err := ExecutePostCommitHooks(ctx, rt, oa, []*Scene{scene}); err != nil {
		return fmt.Errorf("error processing post commit hooks: %w", err)
	}

	slog.Debug("resumed session", "contact", scene.ContactUUID(), "session", session.UUID(), "resume_type", resume.Type(), "elapsed", time.Since(start))

	return nil
}
