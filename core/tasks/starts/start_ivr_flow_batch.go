package starts

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

const TypeStartIVRFlowBatch = "start_ivr_flow_batch"

func init() {
	tasks.RegisterType(TypeStartIVRFlowBatch, func() tasks.Task { return &StartIVRFlowBatchTask{} })
}

// StartIVRFlowBatchTask is the start IVR flow batch task
type StartIVRFlowBatchTask struct {
	*models.FlowStartBatch
}

func (t *StartIVRFlowBatchTask) Type() string {
	return TypeStartIVRFlowBatch
}

// Timeout is the maximum amount of time the task can run for
func (t *StartIVRFlowBatchTask) Timeout() time.Duration {
	return time.Minute * 5
}

func (t *StartIVRFlowBatchTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *StartIVRFlowBatchTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
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

	// ok, we can initiate calls for the remaining contacts
	contacts, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, t.ContactIDs)
	if err != nil {
		return fmt.Errorf("error loading contacts: %w", err)
	}

	// for each contacts, request a call start
	for _, contact := range contacts {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		session, err := ivr.RequestCallWithStart(ctx, rt, oa, t.FlowStartBatch, contact)
		cancel()
		if err != nil {
			slog.Error(fmt.Sprintf("error starting ivr flow for contact: %d and flow: %d", contact.ID(), start.FlowID), "error", err)
			continue
		}
		if session == nil {
			slog.Debug("call start skipped, no suitable channel", "contact_id", contact.ID(), "start_id", start.ID)
			continue
		}
		slog.Debug("requested call for contact", "contact_id", contact.ID(), "status", session.Status(), "start_id", start.ID, "external_id", session.ExternalID())
	}

	// if this is a last batch, mark our start as started
	if t.IsLast {
		if err := start.SetCompleted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking start as complete: %w", err)
		}
	}

	return nil
}
