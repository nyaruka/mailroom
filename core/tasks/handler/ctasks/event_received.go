package ctasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

const TypeEventReceived = "event_received"

func init() {
	handler.RegisterContactTask(TypeEventReceived, func() handler.Task { return &EventReceivedTask{} })
}

type EventReceivedTask struct {
	EventID    models.ChannelEventID   `json:"event_id"`
	EventType  models.ChannelEventType `json:"event_type"`
	ChannelID  models.ChannelID        `json:"channel_id"`
	URNID      models.URNID            `json:"urn_id"`
	OptInID    models.OptInID          `json:"optin_id"`
	Extra      null.Map[any]           `json:"extra"`
	NewContact bool                    `json:"new_contact"`
	CreatedOn  time.Time               `json:"created_on"`
}

func (t *EventReceivedTask) Type() string {
	return TypeEventReceived
}

func (t *EventReceivedTask) UseReadOnly() bool {
	return false
}

func (t *EventReceivedTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	_, err := t.handle(ctx, rt, oa, mc, nil)
	if err != nil {
		return err
	}

	return models.MarkChannelEventHandled(ctx, rt.DB, t.EventID)
}

// Handle let's us reuse this task's code for handling incoming calls.. which we need to perform inline in the IVR web
// handler rather than as a queued task.
func (t *EventReceivedTask) Handle(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact, call *models.Call) (*runner.Scene, error) {
	return t.handle(ctx, rt, oa, mc, call)
}

func (t *EventReceivedTask) handle(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact, call *models.Call) (*runner.Scene, error) {
	channel := oa.ChannelByID(t.ChannelID)

	// if contact is blocked or channel no longer exists, nothing to do
	if mc.Status() == models.ContactStatusBlocked || channel == nil {
		return nil, nil
	}

	if t.EventType == models.EventTypeDeleteContact {
		slog.Info(fmt.Sprintf("NOOP: Handled %s channel event %d", models.EventTypeDeleteContact, t.EventID))

		return nil, nil
	}

	if t.EventType == models.EventTypeStopContact {
		err := mc.Stop(ctx, rt.DB, oa)
		if err != nil {
			return nil, fmt.Errorf("error stopping contact: %w", err)
		}
	}

	if models.ContactSeenEvents[t.EventType] {
		err := mc.UpdateLastSeenOn(ctx, rt.DB, t.CreatedOn)
		if err != nil {
			return nil, fmt.Errorf("error updating contact last_seen_on: %w", err)
		}
	}

	// make sure this URN is our highest priority (this is usually a noop)
	err := mc.UpdatePreferredURN(ctx, rt.DB, oa, t.URNID, channel)
	if err != nil {
		return nil, fmt.Errorf("error changing primary URN: %w", err)
	}

	// build our flow contact
	flowContact, err := mc.EngineContact(oa)
	if err != nil {
		return nil, fmt.Errorf("error creating flow contact: %w", err)
	}

	if t.NewContact {
		err = models.CalculateDynamicGroups(ctx, rt.DB, oa, []*flows.Contact{flowContact})
		if err != nil {
			return nil, fmt.Errorf("unable to initialize new contact: %w", err)
		}
	}

	// do we have associated trigger?
	var trigger *models.Trigger
	var flow *models.Flow

	switch t.EventType {
	case models.EventTypeNewConversation:
		trigger = models.FindMatchingNewConversationTrigger(oa, channel)
	case models.EventTypeReferral:
		referrerID, _ := t.Extra["referrer_id"].(string)
		trigger = models.FindMatchingReferralTrigger(oa, channel, referrerID)
	case models.EventTypeMissedCall:
		trigger = models.FindMatchingMissedCallTrigger(oa, channel)
	case models.EventTypeIncomingCall:
		trigger = models.FindMatchingIncomingCallTrigger(oa, channel, flowContact)
	case models.EventTypeOptIn:
		trigger = models.FindMatchingOptInTrigger(oa, channel)
	case models.EventTypeOptOut:
		trigger = models.FindMatchingOptOutTrigger(oa, channel)
	case models.EventTypeWelcomeMessage, models.EventTypeStopContact, models.EventTypeDeleteContact:
		trigger = nil
	default:
		return nil, fmt.Errorf("unknown channel event type: %s", t.EventType)
	}

	if trigger != nil {
		flow, err = oa.FlowByID(trigger.FlowID())
		if err != nil && err != models.ErrNotFound {
			return nil, fmt.Errorf("error loading flow for trigger: %w", err)
		}
	}

	// no trigger or flow gone, nothing to do
	if flow == nil {
		return nil, nil
	}

	// create our parameters, we just convert this from JSON
	var params *types.XObject
	if t.Extra != nil {
		asJSON, err := json.Marshal(map[string]any(t.Extra))
		if err != nil {
			return nil, fmt.Errorf("unable to marshal extra from channel event: %w", err)
		}
		params, err = types.ReadXObject(asJSON)
		if err != nil {
			return nil, fmt.Errorf("unable to read extra from channel event: %w", err)
		}
	}

	var flowOptIn *flows.OptIn
	if t.EventType == models.EventTypeOptIn || t.EventType == models.EventTypeOptOut {
		optIn := oa.OptInByID(t.OptInID)
		if optIn != nil {
			flowOptIn = oa.SessionAssets().OptIns().Get(optIn.UUID())
		}
	}

	// build our flow trigger
	var trig flows.Trigger
	tb := triggers.NewBuilder(flow.Reference())

	if t.EventType == models.EventTypeIncomingCall {
		trig = tb.Channel(channel.Reference(), triggers.ChannelEventTypeIncomingCall).Build()
	} else if t.EventType == models.EventTypeOptIn && flowOptIn != nil {
		trig = tb.OptIn(flowOptIn, events.NewOptInStarted(flowOptIn, channel.Reference())).Build()
	} else if t.EventType == models.EventTypeOptOut && flowOptIn != nil {
		trig = tb.OptIn(flowOptIn, events.NewOptInStopped(flowOptIn, channel.Reference())).Build()
	} else {
		trig = tb.Channel(channel.Reference(), triggers.ChannelEventType(t.EventType)).WithParams(params).Build()
	}

	var flowCall *flows.Call

	if flow.FlowType() == models.FlowTypeVoice {
		if call != nil {
			// incoming call which already exists
			urn := mc.URNForID(t.URNID)
			flowCall = flows.NewCall(call.UUID(), oa.SessionAssets().Channels().Get(channel.UUID()), urn)
		} else {
			// request outgoing call and wait for callback
			if _, err := ivr.RequestCall(ctx, rt, oa, mc, trig); err != nil {
				return nil, fmt.Errorf("error starting voice flow for contact: %w", err)
			}
			return nil, nil
		}
	}

	scene := runner.NewScene(flowContact, models.NilUserID)
	scene.Call = call

	err = runner.StartSessions(ctx, rt, oa, []*models.Contact{mc}, []*runner.Scene{scene}, flowCall, []flows.Trigger{trig}, flow.FlowType().Interrupts(), models.NilStartID)
	if err != nil {
		return nil, fmt.Errorf("error starting flow for contact: %w", err)
	}

	return scene, nil
}
