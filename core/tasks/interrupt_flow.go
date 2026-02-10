package tasks

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	TypeInterruptFlow = "interrupt_flow"

	// InterruptFlowProgressKey is the redis key prefix used to track the number of sessions remaining to be interrupted
	// for a flow. RapidPro can check this to block further interruption calls until the current one completes.
	InterruptFlowProgressKey = "interrupt_flow_progress"
)

func init() {
	RegisterType(TypeInterruptFlow, func() Task { return &InterruptFlow{} })
}

// InterruptFlow is our task for interrupting all waiting sessions for a given flow. Since there could be many sessions,
// it creates batches of InterruptSessionBatch tasks to do the actual interrupting.
type InterruptFlow struct {
	FlowID models.FlowID `json:"flow_id" validate:"required"`
}

func (t *InterruptFlow) Type() string {
	return TypeInterruptFlow
}

// Timeout is the maximum amount of time the task can run for
func (t *InterruptFlow) Timeout() time.Duration {
	return 10 * time.Minute
}

func (t *InterruptFlow) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *InterruptFlow) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	sessionRefs, err := models.GetWaitingSessionsForFlow(ctx, rt.DB, t.FlowID)
	if err != nil {
		return fmt.Errorf("error getting waiting sessions for flow: %w", err)
	}

	// set a redis key with the total number of sessions being interrupted so that RapidPro can check it
	// to block further interruption calls until this one completes
	if len(sessionRefs) > 0 {
		vc := rt.VK.Get()
		_, err = redis.DoContext(vc, ctx, "SET", fmt.Sprintf("%s:%d", InterruptFlowProgressKey, t.FlowID), len(sessionRefs), "EX", 15*60)
		vc.Close()
		if err != nil {
			return fmt.Errorf("error setting flow interrupt sessions remaining key: %w", err)
		}
	}

	for batch := range slices.Chunk(sessionRefs, interruptSessionBatchSize) {
		task := &InterruptSessionBatch{Sessions: batch, Status: flows.SessionStatusInterrupted, FlowID: t.FlowID}

		if err := Queue(ctx, rt, rt.Queues.Batch, oa.OrgID(), task, false); err != nil {
			return fmt.Errorf("error queueing interrupt session batch task: %w", err)
		}
	}

	return nil
}
