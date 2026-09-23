package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/v26/core/ivr"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const TypeStartFlowBatch = "start_flow_batch"

var startTypeToOrigin = map[models.StartType]string{
	models.StartTypeManual:    "ui",
	models.StartTypeAPI:       "api",
	models.StartTypeAPIZapier: "zapier",
}

func init() {
	RegisterType(TypeStartFlowBatch, func() Task { return &StartFlowBatch{} })
}

// StartFlowBatch is the start flow batch task
type StartFlowBatch struct {
	BatchTask

	*models.FlowStartBatch
}

func (t *StartFlowBatch) Type() string {
	return TypeStartFlowBatch
}

// Timeout is the maximum amount of time the task can run for
func (t *StartFlowBatch) Timeout() time.Duration {
	return time.Minute * 10
}

func (t *StartFlowBatch) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *StartFlowBatch) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error {
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

	// if this start was interrupted, we're done - but tell anyone still watching it
	if start.Status == models.StartStatusInterrupted {
		t.RecordComplete(ctx, rt, taskID)
		publishStartProgress(ctx, rt, oa, start, t.TotalContacts)
		return nil
	}

	// if we're the first batch of the set to start, mark the start itself as started
	if t.RecordStarted(ctx, rt) {
		if err := start.SetStarted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking start as started: %w", err)
		}
		publishStartProgress(ctx, rt, oa, start, t.TotalContacts)
	}

	if err := t.start(ctx, rt, oa, start); err != nil {
		return err
	}

	// mark start as done if this was the last batch to complete
	if t.RecordComplete(ctx, rt, taskID) {
		if err := start.SetCompleted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking start as complete: %w", err)
		}
	}

	// either way this batch moved the start along
	publishStartProgress(ctx, rt, oa, start, t.TotalContacts)

	return nil
}

// visibleStartTypes are the types of start that users see and so may follow the progress of
var visibleStartTypes = []models.StartType{models.StartTypeManual, models.StartTypeAPI, models.StartTypeAPIZapier}

// publishStartProgress publishes a start's status and progress to its flow's socket so that open editors can follow it
// without polling. It's best-effort: failures are logged rather than failing the task whose work has already
// succeeded. Only starts that users can see are published - not those from flow actions or scheduled triggers.
func publishStartProgress(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, start *models.FlowStart, total int) {
	if !slices.Contains(visibleStartTypes, start.StartType) {
		return
	}

	flow, err := oa.FlowByID(start.FlowID)
	if err != nil {
		return // flow is no longer active so nobody has it open
	}

	// the publish itself is a no-op without watchers but the count below isn't free, so check first
	socket := models.FlowSocket(flow.UUID())
	if subscribed, err := rt.Centrifugo.Subscribed(ctx, socket); err != nil || !subscribed[socket] {
		return
	}

	current, err := start.RunCount(ctx, rt.DB)
	if err != nil {
		slog.Error("error getting start progress", "error", err, "start_id", start.ID)
		return
	}

	if err := models.PublishStartProgress(ctx, rt, flow.UUID(), start, current, total); err != nil {
		slog.Error("error publishing start progress", "error", err, "start_id", start.ID)
	}
}

func (t *StartFlowBatch) start(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, start *models.FlowStart) error {
	flow, err := oa.FlowByID(start.FlowID)
	if err == models.ErrNotFound {
		slog.Info("skipping flow start, flow no longer active or archived", "flow_id", start.FlowID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("error loading flow for batch: %w", err)
	}

	// get the user that created this flow start if there was one
	var flowUser *core.User
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

	var history *core.SessionHistory
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
		mcs, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, t.ContactIDs)
		if err != nil {
			return fmt.Errorf("error loading contacts: %w", err)
		}

		// for each contact, request a call start
		for _, mc := range mcs {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			call, err := ivr.RequestCall(ctx, rt, oa, mc, triggerBuilder())
			cancel()
			if err != nil {
				slog.Error("error requesting call for flow start", "contact", mc.UUID(), "start_id", start.ID, "error", err)
				continue
			}
			if call == nil {
				slog.Debug("call start skipped, no suitable channel", "contact", mc.UUID(), "start_id", start.ID)
				continue
			}
		}
	} else {
		mode := models.StartModeBackground
		if flow.FlowType().Interrupts() {
			mode = models.StartModeInterrupt
		}

		_, skipped, err := runner.StartWithLock(ctx, rt, oa, t.ContactIDs, triggerBuilder, mode, t.StartID)
		if err != nil {
			return fmt.Errorf("error starting flow batch: %w", err)
		}

		if len(skipped) > 0 {
			slog.Warn("failed to acquire locks for contacts", "contacts", skipped)
		}
	}

	return nil
}
