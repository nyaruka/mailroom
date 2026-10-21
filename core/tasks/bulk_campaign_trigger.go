package tasks

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/v26/core/ivr"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/utils"
	"github.com/nyaruka/vkutil"
)

const (
	recentFiresCap    = 10                 // number of recent fires we keep per event
	recentFiresExpire = time.Hour * 24 * 7 // how long we keep recent fires
	recentFiresKey    = "recent_campaign_fires:%d"
)

// whether a campaign point fired or was skipped for a contact
type fireOutcome int

const (
	fireOutcomeFired fireOutcome = iota + 1
	fireOutcomeSkipped
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

func (t *BulkCampaignTrigger) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error {
	p := oa.CampaignPointByID(t.PointID)
	if p == nil || p.FireVersion != t.FireVersion {
		slog.Info("skipping campaign trigger for point that no longer exists or has been updated", "point", t.PointID, "fire_version", t.FireVersion)
		return nil
	}

	campaign := oa.SessionAssets().Campaigns().Get(p.Campaign().UUID())
	if campaign == nil {
		return fmt.Errorf("unable to find campaign for point #%d", p.ID)
	}

	skipEvent := func() events.Event {
		return events.NewCampaignSkipped(campaign.Reference(), p.UUID, events.CampaignSkippedReasonExistingSession)
	}

	var outcomes map[models.ContactID]fireOutcome
	var err error

	if p.Type == models.PointTypeFlow {
		outcomes, err = t.triggerFlow(ctx, rt, oa, p, campaign, skipEvent)
	} else {
		outcomes, err = t.triggerBroadcast(ctx, rt, oa, p, skipEvent)
	}
	if err != nil {
		return fmt.Errorf("error triggering campaign point #%d: %w", p.ID, err)
	}

	// store recent fires in redis for this event
	recentSet := vkutil.NewCappedZSet(fmt.Sprintf(recentFiresKey, t.PointID), recentFiresCap, recentFiresExpire)

	vc := rt.VK.Get()
	defer vc.Close()

	numRecorded := 0
	for _, cid := range t.ContactIDs {
		outcome := outcomes[cid]
		if outcome == 0 {
			continue
		}

		// set members need to be unique, so we include a random string
		value := fmt.Sprintf("%s|%d", utils.RandomBase64(10), cid)
		if outcome == fireOutcomeSkipped {
			value += "|skipped"
		}
		score := float64(dates.Now().UnixNano()) / float64(1e9) // score is UNIX time as floating point

		if err := recentSet.Add(ctx, vc, value, score); err != nil {
			return fmt.Errorf("error adding recent trigger to set: %w", err)
		}

		if numRecorded++; numRecorded == recentFiresCap {
			break
		}
	}

	return nil
}

func (t *BulkCampaignTrigger) triggerFlow(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, p *models.CampaignPoint, campaign *core.Campaign, skipEvent runner.EventBuilder) (map[models.ContactID]fireOutcome, error) {
	outcomes := make(map[models.ContactID]fireOutcome, len(t.ContactIDs))

	flow, err := oa.FlowByID(p.FlowID)
	if err == models.ErrNotFound {
		slog.Info("skipping campaign trigger for flow that no longer exists", "point", t.PointID, "flow", p.FlowID)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error loading campaign point flow #%d: %w", p.FlowID, err)
	}

	flowRef := assets.NewFlowReference(flow.UUID(), flow.Name())
	triggerBuilder := func() flows.Trigger {
		return triggers.NewBuilder(flowRef).CampaignFired(events.NewCampaignFired(campaign.Reference(), p.UUID), campaign).Build()
	}

	if flow.FlowType() == models.FlowTypeVoice {
		mcs, err := models.LoadContacts(ctx, rt.ReadonlyDB, oa, t.ContactIDs)
		if err != nil {
			return nil, fmt.Errorf("error loading contacts: %w", err)
		}

		skipped := make([]models.ContactID, 0, len(mcs))

		// for each contact, request a call start
		for _, mc := range mcs {
			if p.StartMode == models.StartModeSkip && mc.CurrentSessionUUID() != "" {
				skipped = append(skipped, mc.ID())
				continue
			}

			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			call, err := ivr.RequestCall(ctx, rt, oa, mc, triggerBuilder())
			cancel()
			if err != nil {
				slog.Error("error requesting call for campaign point", "contact", mc.UUID(), "point", t.PointID, "error", err)
				continue
			}
			if call == nil {
				slog.Debug("call start skipped, no suitable channel", "contact", mc.UUID(), "point", t.PointID)
				continue
			}

			outcomes[mc.ID()] = fireOutcomeFired
		}

		// calls have already been requested so a failure to record skips shouldn't fail the task
		if len(skipped) > 0 {
			unlocked, err := runner.LogWithLock(ctx, rt, oa, skipped, skipEvent)
			if err != nil {
				slog.Error("error recording skipped calls for campaign point", "point", t.PointID, "error", err)
			} else {
				if len(unlocked) > 0 {
					slog.Warn("failed to acquire locks for contacts", "contacts", unlocked)
				}

				for _, cid := range skipped {
					if !slices.Contains(unlocked, cid) {
						outcomes[cid] = fireOutcomeSkipped
					}
				}
			}
		}
	} else {
		scenes, unlocked, err := runner.StartWithLock(ctx, rt, oa, t.ContactIDs, triggerBuilder, p.StartMode, models.NilStartID, skipEvent)
		if err != nil {
			return nil, fmt.Errorf("error starting flow for campaign point #%d: %w", p.ID, err)
		}

		if len(unlocked) > 0 {
			slog.Warn("failed to acquire locks for contacts", "contacts", unlocked)
		}

		for _, scene := range scenes {
			if scene.Session != nil {
				outcomes[scene.DBContact.ID()] = fireOutcomeFired
			} else {
				outcomes[scene.DBContact.ID()] = fireOutcomeSkipped
			}
		}
	}

	return outcomes, nil
}

func (t *BulkCampaignTrigger) triggerBroadcast(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, p *models.CampaignPoint, skipEvent runner.EventBuilder) (map[models.ContactID]fireOutcome, error) {
	bcast := models.NewBroadcast(oa.OrgID(), p.Translations, i18n.Language(p.BaseLanguage), true, nil, t.ContactIDs, nil, "", models.NoExclusions, models.NilUserID)
	bcast.TemplateID = p.TemplateID
	bcast.TemplateVariables = p.TemplateVariables

	scenes, unlocked, err := runner.BroadcastWithLock(ctx, rt, oa, bcast, &models.BroadcastBatch{ContactIDs: t.ContactIDs}, p.StartMode, skipEvent)
	if err != nil {
		return nil, fmt.Errorf("error running campaign point broadcast: %w", err)
	}

	if len(unlocked) > 0 {
		slog.Warn("failed to acquire locks for contacts", "contacts", unlocked)
	}

	outcomes := make(map[models.ContactID]fireOutcome, len(t.ContactIDs))
	for _, scene := range scenes {
		if scene.Broadcast != nil {
			outcomes[scene.DBContact.ID()] = fireOutcomeFired
		} else {
			outcomes[scene.DBContact.ID()] = fireOutcomeSkipped
		}
	}

	return outcomes, nil
}
