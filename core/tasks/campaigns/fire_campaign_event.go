package campaigns

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeFireCampaignEvent is the type of the fire event task
const TypeFireCampaignEvent = "fire_campaign_event"

func init() {
	tasks.RegisterType(TypeFireCampaignEvent, func() tasks.Task { return &FireCampaignEventTask{} })
}

// FireCampaignEventTask is the task to handle firing campaign events
type FireCampaignEventTask struct {
	FireIDs      []models.FireID `json:"fire_ids"`
	EventID      int64           `json:"event_id"`
	EventUUID    string          `json:"event_uuid"`
	FlowUUID     assets.FlowUUID `json:"flow_uuid"`
	CampaignUUID string          `json:"campaign_uuid"`
	CampaignName string          `json:"campaign_name"`
}

func (t *FireCampaignEventTask) Type() string {
	return TypeFireCampaignEvent
}

// Timeout is the maximum amount of time the task can run for
func (t *FireCampaignEventTask) Timeout() time.Duration {
	// base of 5 minutes + one minute per fire
	return time.Minute*5 + time.Minute*time.Duration(len(t.FireIDs))
}

func (t *FireCampaignEventTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform handles firing campaign events
//   - loads the org assets for that event
//   - locks on the contact
//   - loads the contact for that event
//   - creates the trigger for that event
//   - runs the flow that is to be started through our engine
//   - saves the flow run and session resulting from our run
func (t *FireCampaignEventTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	db := rt.DB
	rp := rt.RP
	log := slog.With("comp", "campaign_worker", "event_id", t.EventID)

	// grab all the fires for this event
	fires, err := models.LoadEventFires(ctx, db, t.FireIDs)
	if err != nil {
		// unmark all these fires as fires so they can retry
		rc := rp.Get()
		for _, id := range t.FireIDs {
			rerr := campaignsMarker.Rem(rc, fmt.Sprintf("%d", id))
			if rerr != nil {
				log.Error("error unmarking campaign fire", "error", rerr, "fire_id", id)
			}
		}
		rc.Close()

		// if we had an error, return that
		return fmt.Errorf("error loading event fire from db: %v: %w", t.FireIDs, err)
	}

	// no fires returned
	if len(fires) == 0 {
		log.Info("events already fired, ignoring")
		return nil
	}

	campaign := triggers.NewCampaignReference(triggers.CampaignUUID(t.CampaignUUID), t.CampaignName)

	handled, err := FireCampaignEvents(ctx, rt, oa, fires, t.FlowUUID, campaign, triggers.CampaignEventUUID(t.EventUUID))

	handledSet := make(map[*models.EventFire]bool, len(handled))
	for _, f := range handled {
		handledSet[f] = true
	}

	// any fires that weren't handled are unmarked so they will be retried
	rc := rp.Get()
	defer rc.Close()

	for _, f := range fires {
		if !handledSet[f] {
			rerr := campaignsMarker.Rem(rc, fmt.Sprintf("%d", f.FireID))
			if rerr != nil {
				log.Error("error unmarking campaign fire", "error", rerr, "fire_id", f.FireID)
			}
		}
	}

	if err != nil {
		return fmt.Errorf("error firing campaign events: %d: %w", t.FireIDs, err)
	}

	return nil
}

// FireCampaignEvents tries to handle the given event fires, returning those that were handled (i.e. skipped, fired or deleted)
func FireCampaignEvents(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, fires []*models.EventFire, flowUUID assets.FlowUUID, campaign *triggers.CampaignReference, eventUUID triggers.CampaignEventUUID) ([]*models.EventFire, error) {
	// get the capmaign event object
	dbEvent := oa.CampaignEventByID(fires[0].EventID)
	if dbEvent == nil {
		err := models.DeleteEventFires(ctx, rt.DB, fires)
		if err != nil {
			return nil, fmt.Errorf("error deleting fires for inactive campaign event: %w", err)
		}
		return fires, nil
	}

	// get the flow it references
	flow, err := oa.FlowByUUID(flowUUID)
	if err == models.ErrNotFound {
		err := models.DeleteEventFires(ctx, rt.DB, fires)
		if err != nil {
			return nil, fmt.Errorf("error deleting fires for inactive flow: %w", err)
		}
		return fires, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error loading campaign event flow: %s: %w", flowUUID, err)
	}

	dbFlow := flow.(*models.Flow)

	// figure out which fires should be skipped if any
	firesToSkip := make(map[models.ContactID]*models.EventFire, len(fires))

	if dbEvent.StartMode() == models.StartModeSkip {
		allContactIDs := make([]models.ContactID, len(fires))
		for i := range fires {
			allContactIDs[i] = fires[i].ContactID
		}
		contactsInAFlow, err := models.FilterByWaitingSession(ctx, rt.DB, allContactIDs)
		if err != nil {
			return nil, fmt.Errorf("error finding waiting sessions: %w", err)
		}
		for _, f := range fires {
			if slices.Contains(contactsInAFlow, f.ContactID) {
				firesToSkip[f.ContactID] = f
			}
		}
	}

	// and then which fires should actually be fired
	firesToFire := make(map[models.ContactID]*models.EventFire, len(fires))
	for _, f := range fires {
		if firesToSkip[f.ContactID] == nil {
			firesToFire[f.ContactID] = f
		}
	}

	// mark the skipped fires as skipped and record as handled
	skipped := slices.Collect(maps.Values(firesToSkip))
	err = models.MarkEventsFired(ctx, rt.DB, skipped, time.Now(), models.FireResultSkipped)
	if err != nil {
		return nil, fmt.Errorf("error marking events skipped: %w", err)
	}

	handled := skipped

	// if this is an ivr flow, we need to create a task to perform the start there
	if dbFlow.FlowType() == models.FlowTypeVoice {
		fired := slices.Collect(maps.Values(firesToFire))

		err := handler.TriggerIVRFlow(ctx, rt, oa.OrgID(), dbFlow.ID(), slices.Collect(maps.Keys(firesToFire)), func(ctx context.Context, tx *sqlx.Tx) error {
			return models.MarkEventsFired(ctx, tx, fired, time.Now(), models.FireResultFired)
		})
		if err != nil {
			return nil, fmt.Errorf("error triggering ivr flow start: %w", err)
		}

		handled = append(handled, fired...)

		return handled, nil
	}

	// this is our pre commit callback for our sessions, we'll mark the event fires associated
	// with the passed in sessions as complete in the same transaction
	firedOn := time.Now()
	markFired := func(ctx context.Context, tx *sqlx.Tx, rp *redis.Pool, oa *models.OrgAssets, sessions []*models.Session) error {
		// build up our list of event fire ids based on the session contact ids
		fired := make([]*models.EventFire, 0, len(sessions))
		for _, s := range sessions {
			fired = append(fired, firesToFire[s.ContactID()])
		}

		// mark those events as fired
		err := models.MarkEventsFired(ctx, tx, fired, firedOn, models.FireResultFired)
		if err != nil {
			return fmt.Errorf("error marking events fired: %w", err)
		}

		handled = append(handled, fired...)

		return nil
	}

	// our start options are based on the start mode for our event
	options := &runner.StartOptions{
		Interrupt: dbEvent.StartMode() != models.StartModePassive,
		TriggerBuilder: func(contact *flows.Contact) flows.Trigger {
			return triggers.NewBuilder(oa.Env(), assets.NewFlowReference(flow.UUID(), flow.Name()), contact).Campaign(campaign, eventUUID).Build()
		},
		CommitHook: markFired,
	}

	_, err = runner.StartFlow(ctx, rt, oa, dbFlow, slices.Collect(maps.Keys(firesToFire)), options, models.NilStartID)
	if err != nil {
		slog.Error("error starting flow for campaign event", "error", err, "event", eventUUID)
	}

	return handled, nil
}
