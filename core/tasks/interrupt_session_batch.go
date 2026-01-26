package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	TypeInterruptSessionBatch = "interrupt_session_batch"

	interruptSessionBatchSize = 100
)

func init() {
	RegisterType(TypeInterruptSessionBatch, func() Task { return &InterruptSessionBatch{} })
}

type InterruptSessionBatch struct {
	Sessions []models.SessionRef `json:"sessions" validate:"required"`
}

func (t *InterruptSessionBatch) Type() string {
	return TypeInterruptSessionBatch
}

// Timeout is the maximum amount of time the task can run for
func (t *InterruptSessionBatch) Timeout() time.Duration {
	return time.Hour
}

func (t *InterruptSessionBatch) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *InterruptSessionBatch) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	contactIDs := make([]models.ContactID, len(t.Sessions))
	sessions := make(map[models.ContactID]flows.SessionUUID, len(t.Sessions))
	for i, s := range t.Sessions {
		contactIDs[i] = s.ContactID
		sessions[s.ContactID] = s.UUID
	}

	if _, _, err := runner.InterruptWithLock(ctx, rt, oa, contactIDs, sessions, flows.SessionStatusInterrupted); err != nil {
		return fmt.Errorf("error interrupting contacts for campaign broadcast: %w", err)
	}

	return nil
}
