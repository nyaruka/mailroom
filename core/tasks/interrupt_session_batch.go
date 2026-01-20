package tasks

import (
	"context"
	"time"

	"github.com/nyaruka/mailroom/core/models"
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

	// TODO interrupt sessions that are still the waiting session for the contact

	return nil
}
