package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/clocks"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	commitTimeout = time.Minute
)

var startTypeToOrigin = map[models.StartType]string{
	models.StartTypeManual:    "ui",
	models.StartTypeAPI:       "api",
	models.StartTypeAPIZapier: "zapier",
}

// TriggerBuilder defines the interface for building a trigger for the passed in contact
type TriggerBuilder func(contact *flows.Contact) flows.Trigger

// StartOptions define the various parameters that can be used when starting a flow
type StartOptions struct {
	// Interrupt should be true if we want to interrupt the flows runs for any contact started in this flow
	Interrupt bool

	// TriggerBuilder is the builder that will be used to build a trigger for each contact started in the flow
	TriggerBuilder TriggerBuilder
}

// StartFlowBatch starts the given flow start batch
func StartFlowBatch(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, start *models.FlowStart, batch *models.FlowStartBatch) ([]*models.Session, error) {
	// try to load our flow
	flow, err := oa.FlowByID(start.FlowID)
	if err == models.ErrNotFound {
		slog.Info("skipping flow start, flow no longer active or archived", "flow_id", start.FlowID)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error loading flow: %d: %w", start.FlowID, err)
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
	if !start.Params.IsNull() {
		params, err = types.ReadXObject(start.Params)
		if err != nil {
			return nil, fmt.Errorf("unable to read JSON from flow start params: %w", err)
		}
	}

	var history *flows.SessionHistory
	if !start.SessionHistory.IsNull() {
		history, err = models.ReadSessionHistory(start.SessionHistory)
		if err != nil {
			return nil, fmt.Errorf("unable to read JSON from flow start history: %w", err)
		}
	}

	// whether engine allows some functions is based on whether there is more than one contact being started
	batchStart := batch.TotalContacts > 1

	// this will build our trigger for each contact started
	triggerBuilder := func(contact *flows.Contact) flows.Trigger {
		if !start.ParentSummary.IsNull() {
			tb := triggers.NewBuilder(oa.Env(), flow.Reference(), contact).FlowAction(history, json.RawMessage(start.ParentSummary))
			if batchStart {
				tb = tb.AsBatch()
			}
			return tb.Build()
		}

		tb := triggers.NewBuilder(oa.Env(), flow.Reference(), contact).Manual()
		if !start.Params.IsNull() {
			tb = tb.WithParams(params)
		}
		if batchStart {
			tb = tb.AsBatch()
		}
		return tb.WithUser(flowUser).WithOrigin(startTypeToOrigin[start.StartType]).Build()
	}

	sessions, err := StartWithLock(ctx, rt, oa, batch.ContactIDs, triggerBuilder, flow.FlowType().Interrupts(), batch.StartID, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting flow batch: %w", err)
	}

	return sessions, nil
}

// StartWithLock starts the given contacts in flow sessions after obtaining locks for them.
func StartWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID, triggerBuilder TriggerBuilder, interrupt bool, startID models.StartID, sceneInit func(*Scene)) ([]*models.Session, error) {
	if len(contactIDs) == 0 {
		return nil, nil
	}

	// we now need to grab locks for our contacts so that they are never in two starts or handles at the
	// same time we try to grab locks for up to five minutes, but do it in batches where we wait for one
	// second per contact to prevent deadlocks
	sessions := make([]*models.Session, 0, len(contactIDs))
	remaining := contactIDs
	start := time.Now()

	for len(remaining) > 0 && time.Since(start) < time.Minute*5 {
		if ctx.Err() != nil {
			return sessions, ctx.Err()
		}

		ss, skipped, err := tryToStartWithLock(ctx, rt, oa, remaining, triggerBuilder, interrupt, startID, sceneInit)
		if err != nil {
			return nil, err
		}

		sessions = append(sessions, ss...)
		remaining = skipped // skipped are now our remaining
	}

	if len(remaining) > 0 {
		slog.Warn("failed to acquire locks for contacts", "contacts", remaining)
	}

	return sessions, nil
}

// tries to start the given contacts, returning sessions for those we could, and the ids that were skipped because we
// couldn't get their locks
func tryToStartWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ids []models.ContactID, triggerBuilder TriggerBuilder, interrupt bool, startID models.StartID, sceneInit func(*Scene)) ([]*models.Session, []models.ContactID, error) {
	// try to get locks for these contacts, waiting for up to a second for each contact
	locks, skipped, err := clocks.TryToLock(ctx, rt, oa, ids, time.Second)
	if err != nil {
		return nil, nil, err
	}
	locked := slices.Collect(maps.Keys(locks))

	// whatever happens, we need to unlock the contacts
	defer clocks.Unlock(rt, oa.OrgID(), locks)

	// load our locked contacts
	contacts, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, locked)
	if err != nil {
		return nil, nil, fmt.Errorf("error loading contacts to start: %w", err)
	}

	// build our triggers
	triggers := make([]flows.Trigger, 0, len(locked))
	for _, c := range contacts {
		contact, err := c.FlowContact(oa)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating flow contact: %w", err)
		}
		triggers = append(triggers, triggerBuilder(contact))
	}

	ss, err := StartSessions(ctx, rt, oa, contacts, triggers, interrupt, startID, sceneInit)
	if err != nil {
		return nil, nil, fmt.Errorf("error starting flow for contacts: %w", err)
	}

	return ss, skipped, nil
}

// StartSessions starts the given contacts in flow sessions. It's assumed that the contacts are already locked.
func StartSessions(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contacts []*models.Contact, triggers []flows.Trigger, interrupt bool, startID models.StartID, sceneInit func(*Scene)) ([]*models.Session, error) {
	if len(triggers) == 0 {
		return nil, nil
	}

	start := time.Now()
	sa := oa.SessionAssets()

	// for each trigger start the flow
	sessions := make([]flows.Session, len(triggers))
	sprints := make([]flows.Sprint, len(triggers))
	scenes := make([]*Scene, len(triggers))

	for i, trigger := range triggers {
		session, sprint, err := goflow.Engine(rt).NewSession(ctx, sa, trigger)
		if err != nil {
			return nil, fmt.Errorf("error starting contact %s in flow %s: %w", trigger.Contact().UUID(), trigger.Flow().UUID, err)
		}

		sessions[i] = session
		sprints[i] = sprint
		scenes[i] = NewSessionScene(session, sprint, sceneInit)

		var eventsToHandle []flows.Event

		// if session didn't fail, we need to handle this sprint's events
		if session.Status() != flows.SessionStatusFailed {
			eventsToHandle = append(eventsToHandle, sprints[i].Events()...)
		}

		eventsToHandle = append(eventsToHandle, newSprintEndedEvent(contacts[i], false))

		if err := scenes[i].AddEvents(ctx, rt, oa, eventsToHandle); err != nil {
			return nil, fmt.Errorf("error applying events for session %s: %w", session.UUID(), err)
		}
	}

	// we write our sessions and all their objects in a single transaction
	txCTX, cancel := context.WithTimeout(ctx, commitTimeout*time.Duration(len(sessions)))
	defer cancel()

	tx, err := rt.DB.BeginTxx(txCTX, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}

	// interrupt all our contacts if desired
	if interrupt {
		contactIDs := make([]models.ContactID, len(triggers))
		for i := range triggers {
			contactIDs[i] = models.ContactID(triggers[i].Contact().ID())
		}

		if err := models.InterruptSessionsForContactsTx(txCTX, tx, contactIDs); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("error interrupting contacts: %w", err)
		}
	}

	callIDs := make([]models.CallID, len(triggers))
	for i, s := range scenes {
		if s.Call != nil {
			callIDs[i] = s.Call.ID()
		}
	}

	// write our session to the db
	dbSessions, timeouts, err := models.InsertSessions(txCTX, rt, tx, oa, sessions, sprints, contacts, callIDs, startID)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error interrupting contacts: %w", err)
	}
	for i := range sessions {
		scenes[i].WaitTimeout = timeouts[i]
	}

	// gather all our pre commit events, group them by hook
	if err := ExecutePreCommitHooks(ctx, rt, tx, oa, scenes); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error applying session pre commit hooks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		// retry committing our sessions one at a time
		slog.Debug("failed committing bulk transaction, retrying one at a time", "error", err)

		tx.Rollback()

		dbSessions = nil

		// we failed writing our sessions in one go, try one at a time
		for i, session := range sessions {
			contact, scene, sprint, callID := contacts[i], scenes[i], sprints[i], callIDs[i]

			txCTX, cancel := context.WithTimeout(ctx, commitTimeout)
			defer cancel()

			tx, err := rt.DB.BeginTxx(txCTX, nil)
			if err != nil {
				return nil, fmt.Errorf("error starting transaction for retry: %w", err)
			}

			// interrupt this contact if appropriate
			if interrupt {
				err = models.InterruptSessionsForContactsTx(txCTX, tx, []models.ContactID{models.ContactID(session.Contact().ID())})
				if err != nil {
					tx.Rollback()
					slog.Error("error interrupting contact", "error", err, "contact", session.Contact().UUID())
					continue
				}
			}

			dbSession, timeout, err := models.InsertSessions(txCTX, rt, tx, oa, []flows.Session{session}, []flows.Sprint{sprint}, []*models.Contact{contact}, []models.CallID{callID}, startID)
			if err != nil {
				tx.Rollback()
				slog.Error("error writing session to db", "error", err, "contact", session.Contact().UUID())
				continue
			}
			scene.WaitTimeout = timeout[0]

			// gather all our pre commit events, group them by hook
			if err := ExecutePreCommitHooks(ctx, rt, tx, oa, []*Scene{scene}); err != nil {
				return nil, fmt.Errorf("error applying session pre commit hooks: %w", err)
			}

			if err := tx.Commit(); err != nil {
				tx.Rollback()
				slog.Error("error committing session to db", "error", err, "contact", session.Contact().UUID())
				continue
			}

			dbSessions = append(dbSessions, dbSession[0])
		}
	} else {
		slog.Debug("sessions committed", "count", len(sessions))
	}

	if err := ExecutePostCommitHooks(ctx, rt, oa, scenes); err != nil {
		return nil, fmt.Errorf("error processing post commit hooks: %w", err)
	}

	slog.Debug("started sessions", "count", len(sessions), "elapsed", time.Since(start))

	return dbSessions, nil
}
