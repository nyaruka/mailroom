package ctasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeWaitTimeout = "wait_timeout"

func init() {
	RegisterType(TypeWaitTimeout, func() Task { return &WaitTimeout{} })
}

type WaitTimeout struct {
	SessionUUID flows.SessionUUID `json:"session_uuid"`
	SprintUUID  flows.SprintUUID  `json:"sprint_uuid"`
}

func (t *WaitTimeout) Type() string {
	return TypeWaitTimeout
}

func (t *WaitTimeout) UseReadOnly() bool {
	return true
}

func (t *WaitTimeout) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	log := slog.With("ctask", "wait_timeout", "contact_id", mc.ID(), "session_uuid", t.SessionUUID)

	// if contact's current session has already changed, noop
	if mc.CurrentSessionUUID() != t.SessionUUID {
		log.Debug("skipping as contact's current session has changed")
		return nil
	}

	// build our flow contact
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	session, err := models.GetWaitingSessionForContact(ctx, rt, oa, mc)
	if err != nil {
		return fmt.Errorf("error loading waiting session for contact #%d: %w", mc.ID(), err)
	}

	// if we didn't find a session or if it's been modified since, ignore this task
	if session == nil || session.LastSprintUUID != t.SprintUUID {
		log.Debug("skipping as waiting session has changed")
		return nil
	}

	evt := events.NewWaitTimedOut()

	scene := runner.NewScene(mc, contact)
	if err := scene.AddEvent(ctx, rt, oa, evt, models.NilUserID, ""); err != nil {
		return fmt.Errorf("error adding wait timeout event to scene: %w", err)
	}

	resume := resumes.NewWaitTimeout(evt)

	if err := scene.ResumeSession(ctx, rt, oa, session, resume); err != nil {
		// if we errored, and it's the wait rejecting the timeout event because the flow no longer has a timeout, log and ignore
		var eerr *engine.Error
		if errors.As(err, &eerr) && eerr.Code() == engine.ErrorResumeRejectedByWait && resume.Type() == resumes.TypeWaitTimeout {
			log.Info("ignoring session timeout which is no longer set in flow")
			return nil
		}

		return fmt.Errorf("error resuming flow for timeout: %w", err)
	}

	return scene.Commit(ctx, rt, oa)
}
