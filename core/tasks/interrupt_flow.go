package tasks

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeInterruptFlow = "interrupt_flow"

func init() {
	RegisterType(TypeInterruptFlow, func() Task { return &InterruptFlow{} })
}

type InterruptFlow struct {
	FlowID models.FlowID `json:"flow_id" validate:"required"`
}

func (t *InterruptFlow) Type() string {
	return TypeInterruptFlow
}

// Timeout is the maximum amount of time the task can run for
func (t *InterruptFlow) Timeout() time.Duration {
	return time.Hour
}

func (t *InterruptFlow) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *InterruptFlow) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	sessionRefs, err := models.GetWaitingSessionsForFlow(ctx, rt.DB, t.FlowID)
	if err != nil {
		return fmt.Errorf("error getting waiting sessions for flow: %w", err)
	}

	for batch := range slices.Chunk(sessionRefs, interruptSessionBatchSize) {
		task := &InterruptSessionBatch{Sessions: batch}

		if err := Queue(ctx, rt, rt.Queues.Batch, oa.OrgID(), task, false); err != nil {
			return fmt.Errorf("error queueing interrupt session batch task: %w", err)
		}
	}

	return nil
}
