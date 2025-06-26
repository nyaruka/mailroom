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

	// record run modified times prior to resuming so we can figure out which runs are new or updated
	scene.DBSession = session
	scene.PriorRunModifiedOns = make(map[flows.RunUUID]time.Time, len(fs.Runs()))
	for _, r := range fs.Runs() {
		scene.PriorRunModifiedOns[r.UUID()] = r.ModifiedOn()
	}

	sprint, err := fs.Resume(ctx, resume)
	if err != nil {
		return fmt.Errorf("error resuming flow: %w", err)
	}

	if err := scene.AddSprint(ctx, rt, oa, fs, sprint, true); err != nil {
		return fmt.Errorf("error processing events for session %s: %w", session.UUID(), err)
	}

	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene for resumed session: %w", err)
	}

	slog.Debug("resumed session", "contact", scene.ContactUUID(), "session", session.UUID(), "resume_type", resume.Type(), "elapsed", time.Since(start))

	return nil
}
