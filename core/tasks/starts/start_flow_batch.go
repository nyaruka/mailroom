package starts

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeStartFlowBatch = "start_flow_batch"

var startTypeToOrigin = map[models.StartType]string{
	models.StartTypeManual:    "ui",
	models.StartTypeAPI:       "api",
	models.StartTypeAPIZapier: "zapier",
}

func init() {
	tasks.RegisterType(TypeStartFlowBatch, func() tasks.Task { return &StartFlowBatchTask{} })
}

// StartFlowBatchTask is the start flow batch task
type StartFlowBatchTask struct {
	*models.FlowStartBatch
}

func (t *StartFlowBatchTask) Type() string {
	return TypeStartFlowBatch
}

// Timeout is the maximum amount of time the task can run for
func (t *StartFlowBatchTask) Timeout() time.Duration {
	return time.Minute * 10
}

func (t *StartFlowBatchTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *StartFlowBatchTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	var start *models.FlowStart
	var err error

	// if this batch belongs to a persisted start, fetch it
	if t.StartID != models.NilStartID {
		start, err = models.GetFlowStartByID(ctx, rt.DB, t.StartID)
		if err != nil {
			return fmt.Errorf("error loading flow start for batch: %w", err)
		}
	} else {
		start = t.Start // otherwise use start from the task
	}

	// if this start was interrupted, we're done
	if start.Status == models.StartStatusInterrupted {
		return nil
	}

	// if this is our first batch, mark as started
	if t.IsFirst {
		if err := start.SetStarted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking start as started: %w", err)
		}
	}

	if err := t.start(ctx, rt, oa, start); err != nil {
		return err
	}

	// if this is our last batch, mark start as done
	if t.IsLast {
		if err := start.SetCompleted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking start as complete: %w", err)
		}
	}

	return nil
}

func (t *StartFlowBatchTask) start(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, start *models.FlowStart) error {
	flow, err := oa.FlowByID(start.FlowID)
	if err == models.ErrNotFound {
		slog.Info("skipping flow start, flow no longer active or archived", "flow_id", start.FlowID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("error loading flow for batch: %w", err)
	}

	// get the user that created this flow start if there was one
	var flowUser *flows.User
	if start.CreatedByID != models.NilUserID {
		user := oa.UserByID(start.CreatedByID)
		if user != nil {
			flowUser = oa.SessionAssets().Users().Get(user.UUID())
		}
	}

	var params *types.XObject
	if start.Params != nil {
		params, err = types.ReadXObject(start.Params)
		if err != nil {
			return fmt.Errorf("unable to read JSON from start params: %w", err)
		}
	}

	var history *flows.SessionHistory
	if start.SessionHistory != nil {
		history, err = models.ReadSessionHistory(start.SessionHistory)
		if err != nil {
			return fmt.Errorf("unable to read JSON from start history: %w", err)
		}
	}

	// whether engine allows some functions is based on whether there is more than one contact being started
	batchStart := t.TotalContacts > 1

	// this will build our trigger for each contact started
	triggerBuilder := func() flows.Trigger {
		if start.ParentSummary != nil {
			tb := triggers.NewBuilder(flow.Reference()).FlowAction(history, start.ParentSummary)
			if batchStart {
				tb = tb.AsBatch()
			}
			return tb.Build()
		}

		tb := triggers.NewBuilder(flow.Reference()).Manual().WithParams(params)
		if batchStart {
			tb = tb.AsBatch()
		}
		return tb.WithUser(flowUser).WithOrigin(startTypeToOrigin[start.StartType]).Build()
	}

	if flow.FlowType() == models.FlowTypeVoice {
		contacts, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, t.ContactIDs)
		if err != nil {
			return fmt.Errorf("error loading contacts: %w", err)
		}

		// for each contacts, request a call start
		for _, contact := range contacts {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			call, err := ivr.RequestCall(ctx, rt, oa, contact, triggerBuilder())
			cancel()
			if err != nil {
				slog.Error("error requesting call for flow start", "contact", contact.UUID(), "start_id", start.ID, "error", err)
				continue
			}
			if call == nil {
				slog.Debug("call start skipped, no suitable channel", "contact", contact.UUID(), "start_id", start.ID)
				continue
			}
		}
	} else {
		_, err := runner.StartWithLock(ctx, rt, oa, t.ContactIDs, triggerBuilder, flow.FlowType().Interrupts(), t.StartID)
		if err != nil {
			return fmt.Errorf("error starting flow batch: %w", err)
		}
	}

	return nil
}
