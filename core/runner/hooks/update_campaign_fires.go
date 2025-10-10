package hooks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// UpdateCampaignFires is our hook to update campaign fires
var UpdateCampaignFires runner.PreCommitHook = &updateCampaignFires{}

type updateCampaignFires struct{}

func (h *updateCampaignFires) Order() int { return 50 }

func (h *updateCampaignFires) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// the contact fires to be deleted and inserted
	deletes := make([]*models.FireDelete, 0, 5)
	inserts := make([]*models.ContactFire, 0, 5)

	for s, args := range scenes {
		groupAdds := make(map[models.GroupID]bool)
		groupRemoves := make(map[models.GroupID]bool)
		fieldChanges := make(map[models.FieldID]bool)

		for _, e := range args {
			switch event := e.(type) {

			case *models.GroupAdd:
				groupAdds[event.GroupID] = true
				delete(groupRemoves, event.GroupID)

			case *models.GroupRemove:
				groupRemoves[event.GroupID] = true
				delete(groupAdds, event.GroupID)

			case *events.ContactFieldChanged:
				field := oa.FieldByKey(event.Field.Key)
				if field == nil {
					slog.Debug("unable to find field with key, ignoring for campaign updates", "session", s.SessionUUID(), slog.Group("field", "key", event.Field.Key, "name", event.Field.Name))
					continue
				}
				fieldChanges[field.ID()] = true

			case *events.MsgReceived:
				field := oa.FieldByKey(models.LastSeenOnKey)
				fieldChanges[field.ID()] = true
			}
		}

		// those events that need deleting
		pointsToRemove := make(map[*models.CampaignPoint]bool, len(groupRemoves)+len(fieldChanges))

		// those points we need to add
		pointsToAdd := make(map[*models.CampaignPoint]bool, len(groupAdds)+len(fieldChanges))

		// for every group that was removed, we need to remove all event fires for them
		for g := range groupRemoves {
			for _, c := range oa.CampaignByGroupID(g) {
				for _, e := range c.Points() {
					// only delete events that we qualify for or that were changed
					if e.QualifiesByField(s.Contact) || fieldChanges[e.RelativeToID] {
						pointsToRemove[e] = true
					}
				}
			}
		}

		// for every field that was changed, we need to also remove event fires and recalculate
		for f := range fieldChanges {
			fieldEvents := oa.CampaignPointsByFieldID(f)
			for _, e := range fieldEvents {
				// only recalculate the events if this contact qualifies for this event or this group was removed
				if e.QualifiesByGroup(s.Contact) || groupRemoves[e.Campaign().GroupID()] {
					pointsToRemove[e] = true
					pointsToAdd[e] = true
				}
			}
		}

		// ok, create all our deletes
		for e := range pointsToRemove {
			deletes = append(deletes, &models.FireDelete{ContactID: s.ContactID(), EventID: e.ID, FireVersion: e.FireVersion})
		}

		// add in all the events we qualify for in campaigns we are now part of
		for g := range groupAdds {
			for _, c := range oa.CampaignByGroupID(g) {
				for _, e := range c.Points() {
					pointsToAdd[e] = true
				}
			}
		}

		// ok, for all the unique events we now calculate our fire date
		tz := oa.Env().Timezone()
		now := time.Now()
		for p := range pointsToAdd {
			scheduled, err := p.ScheduleForContact(tz, now, s.Contact)
			if err != nil {
				return fmt.Errorf("error calculating offset: %w", err)
			}

			// no scheduled date? move on
			if scheduled == nil {
				continue
			}

			// ok we have a new fire date, add it to our list of fires to insert
			inserts = append(inserts, models.NewContactFireForCampaign(oa.OrgID(), s.ContactID(), p, *scheduled))
		}
	}

	// delete the campaign fires which are no longer valid
	if err := models.DeleteCampaignFires(ctx, tx, deletes); err != nil {
		return fmt.Errorf("error deleting campaign fires: %w", err)
	}

	// then insert our new ones
	if err := models.InsertContactFires(ctx, tx, inserts); err != nil {
		return fmt.Errorf("error inserting new campaign fires: %w", err)
	}

	return nil
}
