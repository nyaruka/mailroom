package ctasks

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/clogs"
)

const TypeMsgReceived = "msg_received"

func init() {
	handler.RegisterContactTask(TypeMsgReceived, func() handler.Task { return &MsgReceivedTask{} })
}

type MsgReceivedTask struct {
	MsgID         models.MsgID     `json:"msg_id"`
	MsgUUID       flows.MsgUUID    `json:"msg_uuid"`
	MsgExternalID string           `json:"msg_external_id"`
	ChannelID     models.ChannelID `json:"channel_id"`
	URN           urns.URN         `json:"urn"`
	URNID         models.URNID     `json:"urn_id"`
	Text          string           `json:"text"`
	Attachments   []string         `json:"attachments,omitempty"`
	NewContact    bool             `json:"new_contact"`
}

func (t *MsgReceivedTask) Type() string {
	return TypeMsgReceived
}

func (t *MsgReceivedTask) UseReadOnly() bool {
	return !t.NewContact
}

func (t *MsgReceivedTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	result, err := t.handle(ctx, rt, oa, mc)
	if err != nil {
		return err
	}

	flowID := models.NilFlowID
	if result.flow != nil {
		flowID = result.flow.ID()
	}
	ticketID := models.NilTicketID
	if result.ticket != nil {
		ticketID = result.ticket.ID()
	}

	err = models.MarkMessageHandled(ctx, rt.DB, t.MsgID, models.MsgStatusHandled, result.visibility, flowID, ticketID, result.attachments, result.logUUIDs)
	if err != nil {
		return fmt.Errorf("error marking message as handled: %w", err)
	}

	if result.ticket != nil {
		err = models.UpdateTicketLastActivity(ctx, rt.DB, []*models.Ticket{result.ticket})
		if err != nil {
			return fmt.Errorf("error updating last activity for open ticket: %w", err)
		}
	}

	return nil
}

func (t *MsgReceivedTask) handle(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) (*handleResult, error) {
	channel := oa.ChannelByID(t.ChannelID)

	result := &handleResult{
		visibility:  models.VisibilityVisible,
		attachments: make([]utils.Attachment, 0, len(t.Attachments)),
		logUUIDs:    make([]clogs.UUID, 0, len(t.Attachments)),
	}

	// fetch the attachments on the message (i.e. ask courier to fetch them)
	// no channel, no attachments
	if channel != nil {
		for _, attURL := range t.Attachments {
			// if courier has already fetched this attachment, use it as is
			if utils.Attachment(attURL).ContentType() != "" {
				result.attachments = append(result.attachments, utils.Attachment(attURL))
			} else {
				attachment, logUUID, err := msgio.FetchAttachment(ctx, rt, channel, attURL, t.MsgID)
				if err != nil {
					return nil, fmt.Errorf("error fetching attachment '%s': %w", attURL, err)
				}

				result.attachments = append(result.attachments, attachment)
				result.logUUIDs = append(result.logUUIDs, logUUID)
			}
		}
	}

	// if contact is blocked, or channel no longer exists or is disabled, ignore this message but mark it as handled and archived
	if mc.Status() == models.ContactStatusBlocked || channel == nil {
		result.visibility = models.VisibilityArchived
		return result, nil
	}

	// flow will only see the attachments we were able to fetch
	availableAttachments := make([]utils.Attachment, 0, len(result.attachments))
	for _, att := range result.attachments {
		if att.ContentType() != utils.UnavailableType {
			availableAttachments = append(availableAttachments, att)
		}
	}

	msgIn := flows.NewMsgIn(t.MsgUUID, t.URN, channel.Reference(), t.Text, availableAttachments, string(t.MsgExternalID))

	// if we have URNs make sure the message URN is our highest priority (this is usually a noop)
	if len(mc.URNs()) > 0 {
		if err := mc.UpdatePreferredURN(ctx, rt.DB, oa, t.URNID, channel); err != nil {
			return nil, fmt.Errorf("error changing primary URN: %w", err)
		}
	}

	// stopped contact? they are unstopped if they send us an incoming message
	recalcGroups := t.NewContact
	if mc.Status() == models.ContactStatusStopped {
		if err := mc.Unstop(ctx, rt.DB); err != nil {
			return nil, fmt.Errorf("error unstopping contact: %w", err)
		}

		recalcGroups = true
	}

	// build our flow contact
	fc, err := mc.FlowContact(oa)
	if err != nil {
		return nil, fmt.Errorf("error creating flow contact: %w", err)
	}

	// if this is a new or newly unstopped contact, we need to calculate dynamic groups and campaigns
	if recalcGroups {
		err = models.CalculateDynamicGroups(ctx, rt.DB, oa, []*flows.Contact{fc})
		if err != nil {
			return nil, fmt.Errorf("unable to initialize new contact: %w", err)
		}
	}

	// look up any open tickes for this contact and forward this message to that
	ticket, err := models.LoadOpenTicketForContact(ctx, rt.DB, mc)
	if err != nil {
		return nil, fmt.Errorf("unable to look up open tickets for contact: %w", err)
	}

	result.ticket = ticket // will be set on the message

	// find any matching triggers
	trigger, keyword := models.FindMatchingMsgTrigger(oa, channel, fc, t.Text)

	// look for a waiting session for this contact
	var session *models.Session
	var flow *models.Flow

	if mc.CurrentSessionUUID() != "" {
		session, err = models.GetWaitingSessionForContact(ctx, rt, oa, fc, mc.CurrentSessionUUID())
		if err != nil {
			return nil, fmt.Errorf("error loading waiting session for contact #%d: %w", mc.ID(), err)
		}
	}

	if session != nil {
		// if we have a waiting voice session, we want to leave it as is and let this message be handled as inbox below
		if session.SessionType() == models.FlowTypeVoice {
			session = nil
			trigger = nil
		} else {
			// get the flow to be resumed and if it's gone, end the session
			flow, err = oa.FlowByID(session.CurrentFlowID())
			if err == models.ErrNotFound {
				if err := models.ExitSessions(ctx, rt.DB, []flows.SessionUUID{session.UUID()}, models.SessionStatusFailed); err != nil {
					return nil, fmt.Errorf("error ending session %s: %w", session.UUID(), err)
				}
				session = nil
			} else if err != nil {
				return nil, fmt.Errorf("error loading flow for session: %w", err)
			}
		}
	}

	sceneInit := func(scene *runner.Scene) {
		scene.IncomingMsg = &models.MsgInRef{ID: t.MsgID, ExtID: t.MsgExternalID}
	}

	// we found a trigger and their session is nil or doesn't ignore keywords
	if (trigger != nil && trigger.TriggerType() != models.CatchallTriggerType && (flow == nil || !flow.IgnoreTriggers())) ||
		(trigger != nil && trigger.TriggerType() == models.CatchallTriggerType && (flow == nil)) {
		// load our flow
		flow, err = oa.FlowByID(trigger.FlowID())
		if err != nil && err != models.ErrNotFound {
			return nil, fmt.Errorf("error loading flow for trigger: %w", err)
		}

		// trigger flow is still active, start it
		if flow != nil {
			// if this is an IVR flow, we need to trigger that start (which happens in a different queue)
			if flow.FlowType() == models.FlowTypeVoice {
				err = handler.TriggerIVRFlow(ctx, rt, oa, flow, []models.ContactID{mc.ID()})
				if err != nil {
					return nil, fmt.Errorf("error while triggering ivr flow: %w", err)
				}
				return result, nil
			}

			tb := triggers.NewBuilder(oa.Env(), flow.Reference(), fc).Msg(msgIn)
			if keyword != "" {
				tb = tb.WithMatch(&triggers.KeywordMatch{Type: trigger.KeywordMatchType(), Keyword: keyword})
			}

			// otherwise build the trigger and start the flow directly
			trigger := tb.Build()

			_, err = runner.StartFlow(ctx, rt, oa, flow, []*models.Contact{mc}, []flows.Trigger{trigger}, flow.FlowType().Interrupts(), models.NilStartID, models.NilCallID, sceneInit)
			if err != nil {
				return nil, fmt.Errorf("error starting flow for contact: %w", err)
			}

			result.flow = flow
			return result, nil
		}
	}

	// if there is a session, resume it
	if session != nil && flow != nil {
		resume := resumes.NewMsg(oa.Env(), fc, msgIn)
		_, err = runner.ResumeFlow(ctx, rt, oa, session, mc, resume, sceneInit)
		if err != nil {
			return nil, fmt.Errorf("error resuming flow for contact: %w", err)
		}

		result.flow = flow
		return result, nil
	}

	// this message didn't trigger and new sessions or resume any existing ones, so handle as inbox
	msgEvent := events.NewMsgReceived(msgIn)
	fc.SetLastSeenOn(msgEvent.CreatedOn())
	contactEvents := map[*flows.Contact][]flows.Event{fc: {msgEvent}}

	if err := runner.ApplyEvents(ctx, rt, oa, models.NilUserID, contactEvents); err != nil {
		return nil, fmt.Errorf("error handling inbox message events: %w", err)
	}

	return result, nil
}

type handleResult struct {
	visibility  models.MsgVisibility
	flow        *models.Flow
	attachments []utils.Attachment
	ticket      *models.Ticket
	logUUIDs    []clogs.UUID
}
