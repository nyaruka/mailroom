package ctasks

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/ivr"
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
	return false
}

func (t *MsgReceivedTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error {
	outcome, err := t.perform(ctx, rt, oa, mc)

	// TODO cleanup
	if err == nil {
		slog.Warn("msg_received completed", "msg", t.MsgID, "contact", mc.ID(), "outcome", outcome)
	}

	return err
}

type msgOutcome string

const (
	msgOutcomeNonFlow = "non-flow"
	msgOutcomeStart   = "start"
	msgOutcomeResume  = "resume"
)

func (t *MsgReceivedTask) perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) (msgOutcome, error) {
	channel := oa.ChannelByID(t.ChannelID)

	// fetch the attachments on the message (i.e. ask courier to fetch them)
	attachments := make([]utils.Attachment, 0, len(t.Attachments))
	logUUIDs := make([]clogs.UUID, 0, len(t.Attachments))

	// no channel, no attachments
	if channel != nil {
		for _, attURL := range t.Attachments {
			// if courier has already fetched this attachment, use it as is
			if utils.Attachment(attURL).ContentType() != "" {
				attachments = append(attachments, utils.Attachment(attURL))
			} else {
				attachment, logUUID, err := msgio.FetchAttachment(ctx, rt, channel, attURL, t.MsgID)
				if err != nil {
					return "", fmt.Errorf("error fetching attachment '%s': %w", attURL, err)
				}

				attachments = append(attachments, attachment)
				logUUIDs = append(logUUIDs, logUUID)
			}
		}
	}

	// if we have URNs make sure the message URN is our highest priority (this is usually a noop)
	if len(mc.URNs()) > 0 {
		if err := mc.UpdatePreferredURN(ctx, rt.DB, oa, t.URNID, channel); err != nil {
			return "", fmt.Errorf("error changing primary URN: %w", err)
		}
	}

	// stopped contact? they are unstopped if they send us an incoming message
	recalcGroups := t.NewContact
	if mc.Status() == models.ContactStatusStopped {
		if err := mc.Unstop(ctx, rt.DB); err != nil {
			return "", fmt.Errorf("error unstopping contact: %w", err)
		}

		recalcGroups = true
	}

	// build our flow contact
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return "", fmt.Errorf("error creating flow contact: %w", err)
	}

	// if this is a new or newly unstopped contact, we need to calculate dynamic groups and campaigns
	if recalcGroups {
		err = models.CalculateDynamicGroups(ctx, rt.DB, oa, []*flows.Contact{contact})
		if err != nil {
			return "", fmt.Errorf("unable to initialize new contact: %w", err)
		}
	}

	// flow will only see the attachments we were able to fetch
	availableAttachments := make([]utils.Attachment, 0, len(attachments))
	for _, att := range attachments {
		if att.ContentType() != utils.UnavailableType {
			availableAttachments = append(availableAttachments, att)
		}
	}

	msgIn := flows.NewMsgIn(t.MsgUUID, t.URN, channel.Reference(), t.Text, availableAttachments, string(t.MsgExternalID))
	msgEvent := events.NewMsgReceived(msgIn)
	contact.SetLastSeenOn(msgEvent.CreatedOn())

	// look up any open tickes for this contact and forward this message to that
	ticket, err := models.LoadOpenTicketForContact(ctx, rt.DB, mc)
	if err != nil {
		return "", fmt.Errorf("unable to look up open tickets for contact: %w", err)
	}

	scene := runner.NewScene(mc, contact)
	scene.IncomingMsg = &models.MsgInRef{
		ID:          t.MsgID,
		ExtID:       t.MsgExternalID,
		Attachments: attachments,
		Ticket:      ticket,
		LogUUIDs:    logUUIDs,
	}
	if err := scene.AddEvent(ctx, rt, oa, msgEvent, models.NilUserID); err != nil {
		return "", fmt.Errorf("error adding message event to scene: %w", err)
	}

	// if contact is blocked, or channel no longer exists or is disabled, handle non-flow
	if mc.Status() == models.ContactStatusBlocked || channel == nil {
		if err := scene.Commit(ctx, rt, oa); err != nil {
			return "", fmt.Errorf("error handling message for blocked contact or missing channel: %w", err)
		}
		return msgOutcomeNonFlow, nil
	}

	// find any matching triggers
	trigger, keyword := models.FindMatchingMsgTrigger(oa, channel, contact, t.Text)

	// look for a waiting session for this contact
	var session *models.Session
	var flow *models.Flow

	if mc.CurrentSessionUUID() != "" {
		session, err = models.GetWaitingSessionForContact(ctx, rt, oa, contact, mc.CurrentSessionUUID())
		if err != nil {
			return "", fmt.Errorf("error loading waiting session for contact #%d: %w", mc.ID(), err)
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
					return "", fmt.Errorf("error ending session %s: %w", session.UUID(), err)
				}
				session = nil
			} else if err != nil {
				return "", fmt.Errorf("error loading flow for session: %w", err)
			}
		}
	}

	// we found a trigger and their session is nil or doesn't ignore keywords
	if (trigger != nil && trigger.TriggerType() != models.CatchallTriggerType && (flow == nil || !flow.IgnoreTriggers())) ||
		(trigger != nil && trigger.TriggerType() == models.CatchallTriggerType && (flow == nil)) {

		// load flow to check it's still accessible
		flow, err = oa.FlowByID(trigger.FlowID())
		if err != nil && err != models.ErrNotFound {
			return "", fmt.Errorf("error loading flow for trigger: %w", err)
		}

		if flow != nil {
			// create trigger from this message
			tb := triggers.NewBuilder(flow.Reference()).Msg(msgEvent)
			if keyword != "" {
				tb = tb.WithMatch(&triggers.KeywordMatch{Type: trigger.KeywordMatchType(), Keyword: keyword})
			}
			flowTrigger := tb.Build()

			// if this is a voice flow, we request a call and wait for callback
			if flow.FlowType() == models.FlowTypeVoice {
				if _, err := ivr.RequestCall(ctx, rt, oa, mc, flowTrigger); err != nil {
					return "", fmt.Errorf("error starting voice flow for contact: %w", err)
				}

				return msgOutcomeNonFlow, scene.Commit(ctx, rt, oa)
			}

			scene.Interrupt = flow.FlowType().Interrupts()

			if err := runner.StartSessions(ctx, rt, oa, []*runner.Scene{scene}, []flows.Trigger{flowTrigger}); err != nil {
				return "", fmt.Errorf("error starting flow for contact: %w", err)
			}
			return msgOutcomeStart, nil
		}
	}

	// if there is a session, resume it
	if session != nil && flow != nil {
		if err := scene.ResumeSession(ctx, rt, oa, session, resumes.NewMsg(msgEvent)); err != nil {
			return "", fmt.Errorf("error resuming flow for contact: %w", err)
		}

		return msgOutcomeResume, scene.Commit(ctx, rt, oa)
	}

	// this message didn't trigger and new sessions or resume any existing ones, so handle as inbox
	if err := scene.Commit(ctx, rt, oa); err != nil {
		return "", fmt.Errorf("error handling non-flow message: %w", err)
	}
	return msgOutcomeNonFlow, nil
}
