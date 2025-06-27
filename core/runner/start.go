package runner

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/clocks"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	commitTimeout = time.Minute
)

// TriggerBuilder defines the interface for building a trigger for the passed in contact
type TriggerBuilder func() flows.Trigger

// StartWithLock starts the given contacts in flow sessions after obtaining locks for them.
func StartWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID, triggerBuilder TriggerBuilder, interrupt bool, startID models.StartID) ([]*Scene, error) {
	if len(contactIDs) == 0 {
		return nil, nil
	}

	// we now need to grab locks for our contacts so that they are never in two starts or handles at the
	// same time we try to grab locks for up to five minutes, but do it in batches where we wait for one
	// second per contact to prevent deadlocks
	scenes := make([]*Scene, 0, len(contactIDs))
	remaining := contactIDs
	start := time.Now()

	for len(remaining) > 0 && time.Since(start) < time.Minute*5 {
		if ctx.Err() != nil {
			return scenes, ctx.Err()
		}

		ss, skipped, err := tryToStartWithLock(ctx, rt, oa, remaining, triggerBuilder, interrupt, startID)
		if err != nil {
			return nil, err
		}

		scenes = append(scenes, ss...)
		remaining = skipped // skipped are now our remaining
	}

	if len(remaining) > 0 {
		slog.Warn("failed to acquire locks for contacts", "contacts", remaining)
	}

	return scenes, nil
}

// tries to start the given contacts, returning sessions for those we could, and the ids that were skipped because we
// couldn't get their locks
func tryToStartWithLock(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ids []models.ContactID, triggerBuilder TriggerBuilder, interrupt bool, startID models.StartID) ([]*Scene, []models.ContactID, error) {
	// try to get locks for these contacts, waiting for up to a second for each contact
	locks, skipped, err := clocks.TryToLock(ctx, rt, oa, ids, time.Second)
	if err != nil {
		return nil, nil, err
	}
	locked := slices.Collect(maps.Keys(locks))

	// whatever happens, we need to unlock the contacts
	defer clocks.Unlock(ctx, rt, oa, locks)

	// load our locked contacts
	mcs, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, locked)
	if err != nil {
		return nil, nil, fmt.Errorf("error loading contacts to start: %w", err)
	}

	// create scenes and triggers
	scenes := make([]*Scene, 0, len(mcs))
	triggers := make([]flows.Trigger, 0, len(locked))
	for _, mc := range mcs {
		c, err := mc.EngineContact(oa)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating flow contact: %w", err)
		}

		scene := NewScene(mc, c, models.NilUserID)
		scene.StartID = startID
		scene.Interrupt = interrupt

		scenes = append(scenes, scene)
		triggers = append(triggers, triggerBuilder())
	}

	err = StartSessions(ctx, rt, oa, scenes, triggers)
	if err != nil {
		return nil, nil, fmt.Errorf("error starting flow for contacts: %w", err)
	}

	return scenes, skipped, nil
}

// StartSessions starts the given contacts in flow sessions. It's assumed that the contacts are already locked.
func StartSessions(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes []*Scene, triggers []flows.Trigger) error {
	if len(scenes) == 0 {
		return nil
	}

	start := time.Now()

	// for sanity, check that contacts have been locked
	lockCheck, _ := clocks.IsLocked(ctx, rt, oa, scenes[0].DBContact.ID())
	if !lockCheck {
		slog.Error("starting session for contact that isn't locked", "contact", scenes[0].DBContact.ID())
	}

	// for each trigger start the flow
	sessions := make([]flows.Session, len(triggers))
	sprints := make([]flows.Sprint, len(triggers))

	for i, scene := range scenes {
		trigger := triggers[i]

		session, sprint, err := goflow.Engine(rt).NewSession(ctx, oa.SessionAssets(), oa.Env(), scene.Contact, trigger, scene.Call)
		if err != nil {
			return fmt.Errorf("error starting contact %s in flow %s: %w", scene.ContactUUID(), trigger.Flow().UUID, err)
		}

		sessions[i] = session
		sprints[i] = sprint

		if err := scene.AddSprint(ctx, rt, oa, session, sprint, false); err != nil {
			return fmt.Errorf("error adding events for session %s: %w", session.UUID(), err)
		}
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return fmt.Errorf("error committing scenes for started sessions: %w", err)
	}

	slog.Debug("started sessions", "count", len(sessions), "elapsed", time.Since(start))

	return nil
}
