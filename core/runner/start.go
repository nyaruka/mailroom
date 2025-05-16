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

	// CommitHook is the hook that will be called in the transaction where each session is written
	CommitHook models.SessionCommitHook

	// TriggerBuilder is the builder that will be used to build a trigger for each contact started in the flow
	TriggerBuilder TriggerBuilder
}

// StartFlowBatch starts the flow for the passed in org, contacts and flow
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

	options := &StartOptions{
		Interrupt:      flow.FlowType().Interrupts(),
		TriggerBuilder: triggerBuilder,
	}

	sessions, err := StartFlowWithLock(ctx, rt, oa, flow, batch.ContactIDs, options, batch.StartID, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting flow batch: %w", err)
	}

	return sessions, nil
}

// StartFlowWithLock starts the given contacts in the given flow after obtaining locks for them.
func StartFlowWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, flow *models.Flow, contactIDs []models.ContactID, options *StartOptions, startID models.StartID, sceneInit func(*Scene)) ([]*models.Session, error) {
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

		ss, skipped, err := tryToStartWithLock(ctx, rt, oa, flow, remaining, options, startID, sceneInit)
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
func tryToStartWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, flow *models.Flow, ids []models.ContactID, options *StartOptions, startID models.StartID, sceneInit func(*Scene)) ([]*models.Session, []models.ContactID, error) {
	// try to get locks for these contacts, waiting for up to a second for each contact
	locks, skipped, err := models.LockContacts(ctx, rt, oa.OrgID(), ids, time.Second)
	if err != nil {
		return nil, nil, err
	}
	locked := slices.Collect(maps.Keys(locks))

	// whatever happens, we need to unlock the contacts
	defer models.UnlockContacts(rt, oa.OrgID(), locks)

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
		trigger := options.TriggerBuilder(contact)
		triggers = append(triggers, trigger)
	}

	ss, err := StartFlow(ctx, rt, oa, flow, contacts, triggers, options.Interrupt, startID, models.NilCallID, sceneInit, options.CommitHook)
	if err != nil {
		return nil, nil, fmt.Errorf("error starting flow for contacts: %w", err)
	}

	return ss, skipped, nil
}

// StartFlow starts the given contacts in the given flow. It's assumed that the contacts are already locked.
func StartFlow(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets,
	flow *models.Flow, contacts []*models.Contact, triggers []flows.Trigger, interrupt bool,
	startID models.StartID, callID models.CallID, sceneInit func(*Scene), hook models.SessionCommitHook) ([]*models.Session, error) {

	if len(triggers) == 0 {
		return nil, nil
	}

	start := time.Now()
	log := slog.With(slog.Group("flow", "uuid", flow.UUID, "name", flow.Name))
	sa := oa.SessionAssets()

	// for each trigger start the flow
	sessions := make([]flows.Session, 0, len(triggers))
	sprints := make([]flows.Sprint, 0, len(triggers))

	for _, trigger := range triggers {
		log := log.With("contact", trigger.Contact().UUID())

		session, sprint, err := goflow.Engine(rt).NewSession(ctx, sa, trigger)
		if err != nil {
			log.Error("error starting flow", "error", err)
			continue
		}
		log.Debug("new flow session")

		sessions = append(sessions, session)
		sprints = append(sprints, sprint)
	}

	if len(sessions) == 0 {
		return nil, nil
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

	// write our session to the db
	dbSessions, timeouts, err := models.InsertSessions(txCTX, rt, tx, oa, sessions, sprints, contacts, hook, startID, callID)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("error interrupting contacts: %w", err)
	}

	// make scenes and add events to them
	scenes := make([]*Scene, len(dbSessions))
	for i, s := range dbSessions {
		scenes[i] = NewSceneForSession(sessions[i], sprints[i], timeouts[0], sceneInit)

		var eventsToHandle []flows.Event

		// if session didn't fail, we need to handle this sprint's events
		if s.Status() != models.SessionStatusFailed {
			eventsToHandle = append(eventsToHandle, sprints[i].Events()...)
		}

		eventsToHandle = append(eventsToHandle, newSprintEndedEvent(contacts[i], false))

		if err := scenes[i].AddEvents(ctx, rt, oa, eventsToHandle); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("error applying events for session %s: %w", s.UUID(), err)
		}
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

		// we failed writing our sessions in one go, try one at a time
		for i, session := range sessions {
			sprint := sprints[i]
			contact := contacts[i]

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
					log.Error("error interrupting contact", "error", err, "contact", session.Contact().UUID())
					continue
				}
			}

			dbSession, timeout, err := models.InsertSessions(txCTX, rt, tx, oa, []flows.Session{session}, []flows.Sprint{sprint}, []*models.Contact{contact}, hook, startID, callID)
			if err != nil {
				tx.Rollback()
				log.Error("error writing session to db", "error", err, "contact", session.Contact().UUID())
				continue
			}

			var eventsToHandle []flows.Event

			// if session didn't fail, we need to handle this sprint's events
			if session.Status() != flows.SessionStatusFailed {
				eventsToHandle = append(eventsToHandle, sprint.Events()...)
			}

			eventsToHandle = append(eventsToHandle, newSprintEndedEvent(contact, false))
			scene := NewSceneForSession(session, sprint, timeout[0], sceneInit)

			if err := scene.AddEvents(ctx, rt, oa, eventsToHandle); err != nil {
				return nil, fmt.Errorf("error applying events for session %s: %w", session.UUID(), err)
			}

			// gather all our pre commit events, group them by hook
			if err := ExecutePreCommitHooks(ctx, rt, tx, oa, []*Scene{scene}); err != nil {
				return nil, fmt.Errorf("error applying session pre commit hooks: %w", err)
			}

			if err := tx.Commit(); err != nil {
				tx.Rollback()
				log.Error("error committing session to db", "error", err, "contact", session.Contact().UUID())
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

	log.Debug("started sessions", "count", len(dbSessions), "elapsed", time.Since(start))

	return dbSessions, nil
}
