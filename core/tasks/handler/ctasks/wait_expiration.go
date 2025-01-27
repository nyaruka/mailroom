package ctasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeWaitExpiration = "expiration_event"

func init() {
	handler.RegisterContactTask(TypeWaitExpiration, func() handler.Task { return &WaitExpirationTask{} })
}

type WaitExpirationTask struct {
	SessionID  models.SessionID `json:"session_id"`
	ModifiedOn time.Time        `json:"modified_on"` // session modified_on to check it hasn't been changed since we were queued
}

func NewWaitExpiration(sessionID models.SessionID, modifiedOn time.Time) *WaitExpirationTask {
	return &WaitExpirationTask{SessionID: sessionID, ModifiedOn: modifiedOn}
}

func (t *WaitExpirationTask) Type() string {
	return TypeWaitExpiration
}

func (t *WaitExpirationTask) UseReadOnly() bool {
	return true
}

func (t *WaitExpirationTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contact *models.Contact) error {
	// build our flow contact
	flowContact, err := contact.FlowContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	// look for a waiting session for this contact
	session, err := models.FindWaitingSessionForContact(ctx, rt, oa, models.FlowTypeMessaging, flowContact)
	if err != nil {
		return fmt.Errorf("error loading waiting session for contact: %w", err)
	}

	// if we didn't find a session or it is another session or if it's been modified since, ignore this task
	if session == nil || session.ID() != t.SessionID || !session.ModifiedOn().Equal(t.ModifiedOn) {
		return nil
	}

	resume := resumes.NewRunExpiration(oa.Env(), flowContact)

	_, err = runner.ResumeFlow(ctx, rt, oa, session, contact, resume, nil)
	if err != nil {
		return fmt.Errorf("error resuming flow for expiration: %w", err)
	}

	return nil
}
