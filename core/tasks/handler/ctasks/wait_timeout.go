package ctasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeWaitTimeout = "wait_timeout"

func init() {
	handler.RegisterContactTask(TypeWaitTimeout, func() handler.Task { return &WaitTimeoutTask{} })
}

type WaitTimeoutTask struct {
	SessionUUID flows.SessionUUID `json:"session_uuid"`
	SprintUUID  flows.SprintUUID  `json:"sprint_uuid"`
}

func (t *WaitTimeoutTask) Type() string {
	return TypeWaitTimeout
}

func (t *WaitTimeoutTask) UseReadOnly() bool {
	return true
}

func (t *WaitTimeoutTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	log := slog.With("ctask", "wait_timeout", "contact_id", mc.ID(), "session_uuid", t.SessionUUID)

	// build our flow contact
	fc, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	// look for a waiting session for this contact
	session, err := models.GetWaitingSessionForContact(ctx, rt, oa, fc, t.SessionUUID)
	if err != nil {
		return fmt.Errorf("error loading waiting session for contact #%d: %w", mc.ID(), err)
	}

	// if we didn't find a session or it is another session or if it's been modified since, ignore this task
	if session == nil || session.UUID() != t.SessionUUID {
		log.Debug("skipping as waiting session has changed")
		return nil
	}
	if session.LastSprintUUID() != t.SprintUUID {
		log.Info("skipping as session has been modified since", "session_sprint", session.LastSprintUUID(), "task_sprint", t.SprintUUID)
		return nil
	}

	resume := resumes.NewWaitTimeout()

	_, err = runner.ResumeFlow(ctx, rt, oa, session, mc, fc, nil, resume, nil)
	if err != nil {
		// if we errored, and it's the wait rejecting the timeout event because the flow no longer has a timeout, log and ignore
		var eerr *engine.Error
		if errors.As(err, &eerr) && eerr.Code() == engine.ErrorResumeRejectedByWait && resume.Type() == resumes.TypeWaitTimeout {
			log.Info("ignoring session timeout which is no longer set in flow")
			return nil
		}

		return fmt.Errorf("error resuming flow for timeout: %w", err)
	}

	return nil
}
