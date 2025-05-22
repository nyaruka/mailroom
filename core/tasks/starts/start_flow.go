package starts

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	TypeStartFlow = "start_flow"

	startBatchSize = 100
)

func init() {
	tasks.RegisterType(TypeStartFlow, func() tasks.Task { return &StartFlowTask{} })
}

// StartFlowBatchTask is the start flow batch task
type StartFlowTask struct {
	*models.FlowStart
}

func (t *StartFlowTask) Type() string {
	return TypeStartFlow
}

// Timeout is the maximum amount of time the task can run for
func (t *StartFlowTask) Timeout() time.Duration {
	return time.Minute * 60
}

func (t *StartFlowTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

func (t *StartFlowTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	if err := createFlowStartBatches(ctx, rt, oa, t.FlowStart); err != nil {
		t.FlowStart.SetFailed(ctx, rt.DB)

		// if error is user created query error.. don't escalate error to sentry
		isQueryError, _ := contactql.IsQueryError(err)
		if !isQueryError {
			return err
		}
	}

	return nil
}

// creates batches of flow starts for all the unique contacts
func createFlowStartBatches(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, start *models.FlowStart) error {
	flow, err := oa.FlowByID(start.FlowID)
	if err != nil {
		return fmt.Errorf("error loading flow: %w", err)
	}

	var contactIDs []models.ContactID

	if start.CreateContact {
		// if we are meant to create a new contact, do so
		contact, _, err := models.CreateContact(ctx, rt.DB, oa, models.NilUserID, "", i18n.NilLanguage, models.ContactStatusActive, nil)
		if err != nil {
			return fmt.Errorf("error creating new contact: %w", err)
		}
		contactIDs = []models.ContactID{contact.ID()}
	} else {
		// otherwise resolve recipients across contacts, groups, urns etc

		// queries in start_session flow actions only match a single contact
		limit := -1
		if string(start.Query) != "" && start.StartType == models.StartTypeFlowAction {
			limit = 1
		}

		contactIDs, err = search.ResolveRecipients(ctx, rt, oa, start.CreatedByID, flow, &search.Recipients{
			ContactIDs:      start.ContactIDs,
			GroupIDs:        start.GroupIDs,
			URNs:            start.URNs,
			Query:           string(start.Query),
			Exclusions:      start.Exclusions,
			ExcludeGroupIDs: start.ExcludeGroupIDs,
		}, limit)
		if err != nil {
			return fmt.Errorf("error resolving start recipients: %w", err)
		}
	}

	// mark our start as queued
	if err := start.SetQueued(ctx, rt.DB, len(contactIDs)); err != nil {
		return fmt.Errorf("error marking start as queued: %w", err)
	}

	// if there are no contacts to start, mark our start as complete, we are done
	if len(contactIDs) == 0 {
		if err := start.SetCompleted(ctx, rt.DB); err != nil {
			return fmt.Errorf("error marking start as complete: %w", err)
		}
		return nil
	}

	// batches will be processed in the throttled queue unless we're a single contact
	q := tasks.ThrottledQueue
	if len(contactIDs) == 1 {
		q = tasks.HandlerQueue
	}

	// split the contact ids into batches to become batch tasks
	idBatches := slices.Collect(slices.Chunk(contactIDs, startBatchSize))

	rc := rt.RP.Get()
	defer rc.Close()

	for i, idBatch := range idBatches {
		isFirst := (i == 0)
		isLast := (i == len(idBatches)-1)
		batchTask := &StartFlowBatchTask{FlowStartBatch: start.CreateBatch(idBatch, isFirst, isLast, len(contactIDs))}

		if err = tasks.Queue(rc, q, start.OrgID, batchTask, false); err != nil {
			if i == 0 {
				return fmt.Errorf("error queuing flow start batch: %w", err)
			}
			// if we've already queued other batches.. we don't want to error and have the task be retried
			slog.Error("error queuing flow start batch", "error", err)
		}
	}

	return nil
}
