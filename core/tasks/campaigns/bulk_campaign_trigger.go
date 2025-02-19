package campaigns

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/redisx"
)

const (
	recentFiresCap    = 10                 // number of recent fires we keep per event
	recentFiresExpire = time.Hour * 24 * 7 // how long we keep recent fires
	recentFiresKey    = "recent_campaign_fires:%d"
)

// TypeBulkCampaignTrigger is the type of the trigger event task
const TypeBulkCampaignTrigger = "bulk_campaign_trigger"

func init() {
	tasks.RegisterType(TypeBulkCampaignTrigger, func() tasks.Task { return &BulkCampaignTriggerTask{} })
}

// BulkCampaignTriggerTask is the task to handle triggering campaign events
type BulkCampaignTriggerTask struct {
	EventID    models.CampaignEventID `json:"event_id"`
	ContactIDs []models.ContactID     `json:"contact_ids"`
}

func (t *BulkCampaignTriggerTask) Type() string {
	return TypeBulkCampaignTrigger
}

func (t *BulkCampaignTriggerTask) Timeout() time.Duration {
	return time.Minute * 15
}

func (t *BulkCampaignTriggerTask) WithAssets() models.Refresh {
	return models.RefreshCampaigns
}

func (t *BulkCampaignTriggerTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	event := oa.CampaignEventByID(t.EventID)
	if event == nil {
		slog.Info("skipping campaign trigger for event that no longer exists", "event_id", t.EventID)
		return nil
	}

	flow, err := oa.FlowByID(event.FlowID())
	if err == models.ErrNotFound {
		slog.Info("skipping campaign trigger for flow that no longer exists", "event_id", t.EventID, "flow_id", event.FlowID())
		return nil
	}
	if err != nil {
		return fmt.Errorf("error loading campaign event flow #%d: %w", event.FlowID(), err)
	}

	// if event start mode is skip, filter out contact ids that are already in a flow
	// TODO move inside runner.StartFlow so check happens inside contact locks
	contactIDs := t.ContactIDs
	if event.StartMode() == models.StartModeSkip {
		contactIDs, err = models.FilterContactIDsByNotInFlow(ctx, rt.DB, contactIDs)
		if err != nil {
			return fmt.Errorf("error filtering contacts by not in flow: %w", err)
		}
	}

	// if this is an ivr flow, we need to create a task to perform the start there
	if flow.FlowType() == models.FlowTypeVoice {
		err := handler.TriggerIVRFlow(ctx, rt, oa.OrgID(), flow.ID(), contactIDs, nil)
		if err != nil {
			return fmt.Errorf("error triggering ivr flow start: %w", err)
		}
		return nil
	}

	flowRef := assets.NewFlowReference(flow.UUID(), flow.Name())
	campaignRef := triggers.NewCampaignReference(triggers.CampaignUUID(event.Campaign().UUID()), event.Campaign().Name())
	options := &runner.StartOptions{
		Interrupt: event.StartMode() != models.StartModePassive,
		TriggerBuilder: func(contact *flows.Contact) flows.Trigger {
			return triggers.NewBuilder(oa.Env(), flowRef, contact).Campaign(campaignRef, triggers.CampaignEventUUID(event.UUID())).Build()
		},
	}

	_, err = runner.StartFlow(ctx, rt, oa, flow, contactIDs, options, models.NilStartID)
	if err != nil {
		return fmt.Errorf("error starting flow for campaign event #%d: %w", event.ID(), err)
	}

	// store recent fires in redis for this event
	recentSet := redisx.NewCappedZSet(fmt.Sprintf(recentFiresKey, t.EventID), recentFiresCap, recentFiresExpire)

	rc := rt.RP.Get()
	defer rc.Close()

	for _, cid := range contactIDs[:min(recentFiresCap, len(contactIDs))] {
		// set members need to be unique, so we include a random string
		value := fmt.Sprintf("%s|%d", redisx.RandomBase64(10), cid)
		score := float64(dates.Now().UnixNano()) / float64(1e9) // score is UNIX time as floating point

		err := recentSet.Add(rc, value, score)
		if err != nil {
			return fmt.Errorf("error adding recent trigger to set: %w", err)
		}
	}

	return nil
}
