package runner

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

// Interrupt interrupts the sessions for the given contacts
// TODO rework to share contact locking code with bulk starts?
func Interrupt(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactIDs []models.ContactID) error {
	// load our contacts
	mcs, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, contactIDs)
	if err != nil {
		return fmt.Errorf("error loading contacts to start: %w", err)
	}

	// make scenes
	scenes := make([]*Scene, 0, len(mcs))
	for _, mc := range mcs {
		c, err := mc.EngineContact(oa)
		if err != nil {
			return fmt.Errorf("error creating flow contact: %w", err)
		}

		scene := NewScene(mc, c)
		scenes = append(scenes, scene)
	}

	if err := interruptScenes(ctx, rt, oa, scenes); err != nil {
		return fmt.Errorf("error interrupting existing sessions: %w", err)
	}

	if err := BulkCommit(ctx, rt, oa, scenes); err != nil {
		return fmt.Errorf("error committing interruption scenes: %w", err)
	}

	return nil
}

// adds contact interruption to the given scenes
func interruptScenes(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes []*Scene) error {
	sessions := make(map[flows.SessionUUID]*Scene, len(scenes))
	for _, s := range scenes {
		if s.DBContact.CurrentSessionUUID() != "" {
			sessions[s.DBContact.CurrentSessionUUID()] = s
		}
	}
	if len(sessions) == 0 {
		return nil // nothing to do
	}

	runRefs, err := models.GetActiveAndWaitingRuns(ctx, rt, slices.Collect(maps.Keys(sessions)))
	if err != nil {
		return fmt.Errorf("error getting active runs for waiting sessions: %w", err)
	}

	for _, s := range scenes {
		if s.DBContact.CurrentSessionUUID() != "" {
			if err := s.AddEvent(ctx, rt, oa, newContactInterruptedEvent(), models.NilUserID); err != nil {
				return fmt.Errorf("error adding contact interrupted event: %w", err)
			}

			for _, run := range runRefs[s.DBContact.CurrentSessionUUID()] {
				if err := s.AddEvent(ctx, rt, oa, events.NewRunEnded(run.UUID, run.Flow, flows.RunStatusInterrupted), models.NilUserID); err != nil {
					return fmt.Errorf("error adding run ended event: %w", err)
				}
			}
		}
	}

	return nil
}
