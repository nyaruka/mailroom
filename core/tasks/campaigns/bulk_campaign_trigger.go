package campaigns

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/vkutil"
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
	EventID     models.CampaignEventID `json:"event_id"`
	FireVersion int                    `json:"fire_version"`
	ContactIDs  []models.ContactID     `json:"contact_ids"`
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
	ce := oa.CampaignEventByID(t.EventID)
	if ce == nil || ce.FireVersion != t.FireVersion {
		slog.Info("skipping campaign trigger for event that no longer exists or has been updated", "event_id", t.EventID, "fire_version", t.FireVersion)
		return nil
	}

	// if event start mode is skip, filter out contact ids that are already in a flow
	// TODO move inside runner.StartFlow so check happens inside contact locks
	contactIDs := t.ContactIDs
	if ce.StartMode == models.CampaignEventModeSkip {
		var err error
		contactIDs, err = models.FilterContactIDsByNotInFlow(ctx, rt.DB, contactIDs)
		if err != nil {
			return fmt.Errorf("error filtering contacts by not in flow: %w", err)
		}
	}
	if len(contactIDs) == 0 {
		return nil
	}

	if ce.EventType == models.CampaignEventTypeFlow {
		if err := t.triggerFlow(ctx, rt, oa, ce, contactIDs); err != nil {
			return err
		}
	} else {
		if err := t.triggerBroadcast(ctx, rt, oa, ce, contactIDs); err != nil {
			return err
		}
	}

	// store recent fires in redis for this event
	recentSet := vkutil.NewCappedZSet(fmt.Sprintf(recentFiresKey, t.EventID), recentFiresCap, recentFiresExpire)

	rc := rt.RP.Get()
	defer rc.Close()

	for _, cid := range contactIDs[:min(recentFiresCap, len(contactIDs))] {
		// set members need to be unique, so we include a random string
		value := fmt.Sprintf("%s|%d", vkutil.RandomBase64(10), cid)
		score := float64(dates.Now().UnixNano()) / float64(1e9) // score is UNIX time as floating point

		err := recentSet.Add(ctx, rc, value, score)
		if err != nil {
			return fmt.Errorf("error adding recent trigger to set: %w", err)
		}
	}

	return nil
}

func (t *BulkCampaignTriggerTask) triggerFlow(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ce *models.CampaignEvent, contactIDs []models.ContactID) error {
	flow, err := oa.FlowByID(ce.FlowID)
	if err == models.ErrNotFound {
		slog.Info("skipping campaign trigger for flow that no longer exists", "event_id", t.EventID, "flow_id", ce.FlowID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("error loading campaign event flow #%d: %w", ce.FlowID, err)
	}

	campaign := oa.SessionAssets().Campaigns().Get(ce.Campaign().UUID())
	if campaign == nil {
		return fmt.Errorf("unable to find campaign for event #%d: %w", ce.ID, err)
	}

	flowRef := assets.NewFlowReference(flow.UUID(), flow.Name())
	triggerBuilder := func(contact *flows.Contact) flows.Trigger {
		return triggers.NewBuilder(oa.Env(), flowRef, contact).Campaign(campaign, ce.UUID).Build()
	}

	if flow.FlowType() == models.FlowTypeVoice {
		contacts, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, t.ContactIDs)
		if err != nil {
			return fmt.Errorf("error loading contacts: %w", err)
		}

		// for each contacts, request a call start
		for _, contact := range contacts {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			call, err := ivr.RequestCall(ctx, rt, oa, contact, triggerBuilder(nil))
			cancel()
			if err != nil {
				slog.Error("error requesting call for campaign event", "contact", contact.UUID(), "event_id", t.EventID, "error", err)
				continue
			}
			if call == nil {
				slog.Debug("call start skipped, no suitable channel", "contact", contact.UUID(), "event_id", t.EventID)
				continue
			}
		}
	} else {
		interrupt := ce.StartMode != models.CampaignEventModePassive

		_, err = runner.StartWithLock(ctx, rt, oa, contactIDs, triggerBuilder, interrupt, models.NilStartID, nil)
		if err != nil {
			return fmt.Errorf("error starting flow for campaign event #%d: %w", ce.ID, err)
		}
	}

	return nil
}

func (t *BulkCampaignTriggerTask) triggerBroadcast(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ce *models.CampaignEvent, contactIDs []models.ContactID) error {
	// interrupt the contacts if desired
	if ce.StartMode != models.CampaignEventModePassive {
		if _, err := models.InterruptSessionsForContacts(ctx, rt.DB, contactIDs); err != nil {
			return fmt.Errorf("error interrupting contacts: %w", err)
		}
	}

	bcast := models.NewBroadcast(oa.OrgID(), ce.Translations, i18n.Language(ce.BaseLanguage), true, models.NilOptInID, nil, contactIDs, nil, "", models.NoExclusions, models.NilUserID)
	sends, err := bcast.CreateMessages(ctx, rt, oa, &models.BroadcastBatch{ContactIDs: contactIDs})
	if err != nil {
		return fmt.Errorf("error creating campaign event messages: %w", err)
	}

	msgio.QueueMessages(ctx, rt, sends)
	return nil
}
