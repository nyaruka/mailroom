package ctasks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

const TypeEventReceived = "event_received"

func init() {
	realtime.RegisterContactTask(TypeEventReceived, func() realtime.Task { return &EventReceivedTask{} })
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
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return nil, fmt.Errorf("error creating flow contact: %w", err)
	}

	if t.NewContact {
		err = models.CalculateDynamicGroups(ctx, rt.DB, oa, []*flows.Contact{contact})
		if err != nil {
			return nil, fmt.Errorf("unable to initialize new contact: %w", err)
		}
	}

	var flowCall *flows.Call
	if call != nil {
		flowCall = flows.NewCall(call.UUID(), oa.SessionAssets().Channels().Get(channel.UUID()), mc.URNForID(t.URNID))
	}

	trig, flowType, err := t.findTrigger(oa, channel, contact, flowCall)
	if err != nil {
		return nil, err
	}

	if trig != nil && flowType == models.FlowTypeVoice {
		if call == nil {
			// request outgoing call and wait for callback
			if _, err := ivr.RequestCall(ctx, rt, oa, mc, trig); err != nil {
				return nil, fmt.Errorf("error starting voice flow for contact: %w", err)
			}
			return nil, nil
		}
	}

	scene := runner.NewScene(mc, contact)
	scene.DBCall = call
	scene.Call = flowCall

	if trig != nil {
		if event := trig.Event(); event != nil {
			if err := scene.AddEvent(ctx, rt, oa, event, models.NilUserID); err != nil {
				return nil, fmt.Errorf("error adding trigger event to scene: %w", err)
			}
		}

		if err := scene.StartSession(ctx, rt, oa, trig, flowType.Interrupts()); err != nil {
			return nil, fmt.Errorf("error starting session for contact %s: %w", scene.ContactUUID(), err)
		}
	}

	if err := scene.Commit(ctx, rt, oa); err != nil {
		return nil, fmt.Errorf("error committing scene for contact %s: %w", scene.ContactUUID(), err)
	}

	return scene, nil
}

func (t *EventReceivedTask) findTrigger(oa *models.OrgAssets, ch *models.Channel, c *flows.Contact, call *flows.Call) (flows.Trigger, models.FlowType, error) {
	var mtrig *models.Trigger

	switch t.EventType {
	case models.EventTypeNewConversation:
		mtrig = models.FindMatchingNewConversationTrigger(oa, ch)
	case models.EventTypeReferral:
		referrerID, _ := t.Extra["referrer_id"].(string)
		mtrig = models.FindMatchingReferralTrigger(oa, ch, referrerID)
	case models.EventTypeMissedCall:
		mtrig = models.FindMatchingMissedCallTrigger(oa, ch)
	case models.EventTypeIncomingCall:
		mtrig = models.FindMatchingIncomingCallTrigger(oa, ch, c)
	case models.EventTypeOptIn:
		mtrig = models.FindMatchingOptInTrigger(oa, ch)
	case models.EventTypeOptOut:
		mtrig = models.FindMatchingOptOutTrigger(oa, ch)
	case models.EventTypeWelcomeMessage, models.EventTypeStopContact, models.EventTypeDeleteContact:
		return nil, "", nil
	default:
		return nil, "", fmt.Errorf("unknown channel event type: %s", t.EventType)
	}

	// check flow still exists
	var flow *models.Flow
	var err error
	if mtrig != nil {
		flow, err = oa.FlowByID(mtrig.FlowID())
		if err != nil && err != models.ErrNotFound {
			return nil, "", fmt.Errorf("error loading flow for trigger: %w", err)
		}
	}

	// no trigger or flow gone, nothing to do
	if flow == nil {
		return nil, "", nil
	}

	var flowOptIn *flows.OptIn
	if t.EventType == models.EventTypeOptIn || t.EventType == models.EventTypeOptOut {
		optIn := oa.OptInByID(t.OptInID)
		if optIn != nil {
			flowOptIn = oa.SessionAssets().OptIns().Get(optIn.UUID())
		}
	}

	// build engine trigger
	var trig flows.Trigger
	tb := triggers.NewBuilder(flow.Reference())

	if t.EventType == models.EventTypeIncomingCall {
		trig = tb.CallReceived(events.NewCallReceived(call)).Build()
	} else if t.EventType == models.EventTypeMissedCall {
		trig = tb.CallMissed(events.NewCallMissed(ch.Reference())).Build()
	} else if t.EventType == models.EventTypeOptIn && flowOptIn != nil {
		trig = tb.OptInStarted(events.NewOptInStarted(flowOptIn.Reference(), ch.Reference()), flowOptIn).Build()
	} else if t.EventType == models.EventTypeOptOut && flowOptIn != nil {
		trig = tb.OptInStopped(events.NewOptInStopped(flowOptIn.Reference(), ch.Reference()), flowOptIn).Build()
	} else if t.EventType == models.EventTypeNewConversation {
		trig = tb.ChatStarted(events.NewChatStarted(ch.Reference(), nil)).Build()
	} else if t.EventType == models.EventTypeReferral {
		var params map[string]string
		if t.Extra != nil {
			params = make(map[string]string, len(t.Extra))
			for k, v := range t.Extra {
				if vs, ok := v.(string); ok {
					params[k] = vs
				}
			}
		}
		trig = tb.ChatStarted(events.NewChatStarted(ch.Reference(), params)).Build()
	}

	return trig, flow.FlowType(), nil
}
