package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
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
	RegisterType(TypeBulkCampaignTrigger, func() Task { return &BulkCampaignTrigger{} })
}

// BulkCampaignTrigger is the task to handle triggering campaign fires
type BulkCampaignTrigger struct {
	PointID     models.PointID     `json:"point_id"`
	FireVersion int                `json:"fire_version"`
	ContactIDs  []models.ContactID `json:"contact_ids"`
}

func (t *BulkCampaignTrigger) Type() string {
	return TypeBulkCampaignTrigger
}

func (t *BulkCampaignTrigger) Timeout() time.Duration {
	return time.Minute * 15
}

func (t *BulkCampaignTrigger) WithAssets() models.Refresh {
	return models.RefreshCampaigns
}

func (t *BulkCampaignTrigger) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	p := oa.CampaignPointByID(t.PointID)
	if p == nil || p.FireVersion != t.FireVersion {
		slog.Info("skipping campaign trigger for point that no longer exists or has been updated", "point", t.PointID, "fire_version", t.FireVersion)
		return nil
	}

	// if start mode is skip, filter out contact ids that are already in a flow
	// TODO move inside runner.StartFlow so check happens inside contact locks
	contactIDs := t.ContactIDs
	if p.StartMode == models.PointModeSkip {
		var err error
		contactIDs, err = models.FilterContactIDsByNotInFlow(ctx, rt.DB, contactIDs)
		if err != nil {
			return fmt.Errorf("error filtering contacts by not in flow: %w", err)
		}
	}
	if len(contactIDs) == 0 {
		return nil
	}

	if p.Type == models.PointTypeFlow {
		if err := t.triggerFlow(ctx, rt, oa, p, contactIDs); err != nil {
			return err
		}
	} else {
		if err := t.triggerBroadcast(ctx, rt, oa, p, contactIDs); err != nil {
			return err
		}
	}

	// store recent fires in redis for this event
	recentSet := vkutil.NewCappedZSet(fmt.Sprintf(recentFiresKey, t.PointID), recentFiresCap, recentFiresExpire)

	vc := rt.VK.Get()
	defer vc.Close()

	for _, cid := range contactIDs[:min(recentFiresCap, len(contactIDs))] {
		// set members need to be unique, so we include a random string
		value := fmt.Sprintf("%s|%d", vkutil.RandomBase64(10), cid)
		score := float64(dates.Now().UnixNano()) / float64(1e9) // score is UNIX time as floating point

		err := recentSet.Add(ctx, vc, value, score)
		if err != nil {
			return fmt.Errorf("error adding recent trigger to set: %w", err)
		}
	}

	return nil
}

func (t *BulkCampaignTrigger) triggerFlow(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, p *models.CampaignPoint, contactIDs []models.ContactID) error {
	flow, err := oa.FlowByID(p.FlowID)
	if err == models.ErrNotFound {
		slog.Info("skipping campaign trigger for flow that no longer exists", "point", t.PointID, "flow", p.FlowID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("error loading campaign point flow #%d: %w", p.FlowID, err)
	}

	campaign := oa.SessionAssets().Campaigns().Get(p.Campaign().UUID())
	if campaign == nil {
		return fmt.Errorf("unable to find campaign for point #%d: %w", p.ID, err)
	}

	flowRef := assets.NewFlowReference(flow.UUID(), flow.Name())
	triggerBuilder := func() flows.Trigger {
		return triggers.NewBuilder(flowRef).CampaignFired(events.NewCampaignFired(campaign, p.UUID), campaign).Build()
	}

	if flow.FlowType() == models.FlowTypeVoice {
		contacts, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, t.ContactIDs)
		if err != nil {
			return fmt.Errorf("error loading contacts: %w", err)
		}

		// for each contacts, request a call start
		for _, contact := range contacts {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			call, err := ivr.RequestCall(ctx, rt, oa, contact, triggerBuilder())
			cancel()
			if err != nil {
				slog.Error("error requesting call for campaign point", "contact", contact.UUID(), "point", t.PointID, "error", err)
				continue
			}
			if call == nil {
				slog.Debug("call start skipped, no suitable channel", "contact", contact.UUID(), "point", t.PointID)
				continue
			}
		}
	} else {
		interrupt := p.StartMode != models.PointModePassive

		_, skipped, err := runner.StartWithLock(ctx, rt, oa, contactIDs, triggerBuilder, interrupt, models.NilStartID)
		if err != nil {
			return fmt.Errorf("error starting flow for campaign point #%d: %w", p.ID, err)
		}

		if len(skipped) > 0 {
			slog.Warn("failed to acquire locks for contacts", "contacts", skipped)
		}
	}

	return nil
}

func (t *BulkCampaignTrigger) triggerBroadcast(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, p *models.CampaignPoint, contactIDs []models.ContactID) error {
	// interrupt the contacts if desired
	if p.StartMode != models.PointModePassive {
		if _, _, err := runner.InterruptWithLock(ctx, rt, oa, contactIDs, nil, flows.SessionStatusInterrupted); err != nil {
			return fmt.Errorf("error interrupting contacts for campaign broadcast: %w", err)
		}
	}

	bcast := models.NewBroadcast(oa.OrgID(), p.Translations, i18n.Language(p.BaseLanguage), true, models.NilOptInID, nil, contactIDs, nil, "", models.NoExclusions, models.NilUserID)

	if err := runner.Broadcast(ctx, rt, oa, bcast, &models.BroadcastBatch{ContactIDs: contactIDs}); err != nil {
		return fmt.Errorf("error running campaign point broadcast: %w", err)
	}

	return nil
}
