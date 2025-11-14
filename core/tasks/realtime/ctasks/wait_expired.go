package ctasks

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeWaitExpired = "wait_expired"

func init() {
	realtime.RegisterContactTask(TypeWaitExpired, func() realtime.Task { return &WaitExpiredTask{} })
}

type WaitExpiredTask struct {
	SessionUUID flows.SessionUUID `json:"session_uuid"`
	SprintUUID  flows.SprintUUID  `json:"sprint_uuid"`
}

func (t *WaitExpiredTask) Type() string {
	return TypeWaitExpired
}

func (t *WaitExpiredTask) UseReadOnly() bool {
	return true
}

func (t *WaitExpiredTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	log := slog.With("ctask", "wait_expired", "contact_id", mc.ID(), "session_uuid", t.SessionUUID)

	// build our flow contact
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	// look for a waiting session for this contact
	session, err := models.GetWaitingSessionForContact(ctx, rt, oa, contact, t.SessionUUID)
	if err != nil {
		return fmt.Errorf("error loading waiting session for contact #%d: %w", mc.ID(), err)
	}

	// if we didn't find a session or it is another session or if it's been modified since, ignore this task
	if session == nil || session.UUID != t.SessionUUID || session.LastSprintUUID != t.SprintUUID {
		log.Debug("skipping as waiting session has changed")
		return nil
	}

	evt := events.NewWaitExpired()

	if session.SessionType == models.FlowTypeVoice {
		// load our call
		call, err := models.GetCallByUUID(ctx, rt.DB, oa.OrgID(), session.CallUUID)
		if err != nil {
			return fmt.Errorf("error loading call for voice session: %w", err)
		}

		// hang up our call
		clog, err := ivr.HangupCall(ctx, rt, call)
		if err != nil {
			return fmt.Errorf("error hanging up call for voice session: %w", err)
		}

		if clog != nil {
			if _, err := rt.Writers.Main.Queue(clog); err != nil {
				return fmt.Errorf("error queuing IVR channel log to writer: %w", err)
			}
		}

		if err := runner.Interrupt(ctx, rt, oa, []models.ContactID{mc.ID()}, flows.SessionStatusExpired); err != nil {
			return fmt.Errorf("error expiring sessions for expired calls: %w", err)
		}

		return nil

	}

	scene := runner.NewScene(mc, contact)
	if err := scene.AddEvent(ctx, rt, oa, evt, models.NilUserID); err != nil {
		return fmt.Errorf("error adding wait expired event to scene: %w", err)
	}

	if err := scene.ResumeSession(ctx, rt, oa, session, resumes.NewWaitExpiration(evt)); err != nil {
		return fmt.Errorf("error resuming flow for expiration: %w", err)
	}

	return scene.Commit(ctx, rt, oa)
}
