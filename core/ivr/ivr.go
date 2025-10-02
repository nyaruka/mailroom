package ivr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/clogs"
)

type CallID string

const (
	NilCallID     = CallID("")
	NilAttachment = utils.Attachment("")

	// ErrorMessage that is spoken to an IVR user if an error occurs
	ErrorMessage = "An error has occurred, please try again later."

	ActionStart  = "start"
	ActionResume = "resume"
	ActionStatus = "status"
)

// CallbackParams is our form for what fields we expect in IVR callbacks
type CallbackParams struct {
	Action   string         `form:"action"     validate:"required"`
	CallUUID flows.CallUUID `form:"call"       validate:"required"`
}

func (p *CallbackParams) Encode() string {
	return url.Values{"action": []string{p.Action}, "call": []string{string(p.CallUUID)}}.Encode()
}

// HangupCall hangs up the passed in call also taking care of updating the status of our call in the process
func HangupCall(ctx context.Context, rt *runtime.Runtime, call *models.Call) (*models.ChannelLog, error) {
	// no matter what mark our call as failed
	defer call.SetFailed(ctx, rt.DB)

	// load our org assets
	oa, err := models.GetOrgAssets(ctx, rt, call.OrgID())
	if err != nil {
		return nil, fmt.Errorf("error loading org assets: %w", err)
	}

	// and our channel
	channel := oa.ChannelByID(call.ChannelID())
	if channel == nil {
		return nil, fmt.Errorf("unable to load channel: %w", err)
	}

	// create the right service
	svc, err := GetService(channel)
	if err != nil {
		return nil, fmt.Errorf("unable to create IVR service: %w", err)
	}

	clog := models.NewChannelLog(models.ChannelLogTypeIVRHangup, channel, svc.RedactValues(channel))
	defer clog.End()

	// try to request our call hangup
	trace, err := svc.HangupCall(call.ExternalID())
	if trace != nil {
		clog.HTTP(trace)
	}
	if err != nil {
		clog.Error(&clogs.Error{Message: err.Error()})
	}

	if err := call.AttachLog(ctx, rt.DB, clog); err != nil {
		slog.Error("error attaching ivr channel log", "error", err)
	}

	return clog, err
}

// RequestCall creates a new outgoing call and makes a request to the service to start it
func RequestCall(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contact *models.Contact, trigger flows.Trigger) (*models.Call, error) {
	// find a tel URL for the contact
	var telURN *models.ContactURN
	for _, u := range contact.URNs() {
		if u.Scheme == urns.Phone.Prefix {
			telURN = u
		}
	}

	if telURN == nil {
		return nil, fmt.Errorf("no tel URN on contact, cannot start IVR flow")
	}

	// build our channel assets, we need these to calculate the preferred channel for a call
	channels, err := oa.Channels()
	if err != nil {
		return nil, fmt.Errorf("unable to load channels for org: %w", err)
	}
	ca := flows.NewChannelAssets(channels)

	// TODO find better way to turn models.ContactURN into flows.ContactURN
	telURNEncoded, err := telURN.Encode(oa)
	if err != nil {
		return nil, fmt.Errorf("error encoding URN: %w", err)
	}

	urn, err := flows.ParseRawURN(ca, telURNEncoded, assets.IgnoreMissing)
	if err != nil {
		return nil, fmt.Errorf("unable to parse URN: %s: %w", telURN, err)
	}

	// get the channel to use for outgoing calls
	callChannel := ca.GetForURN(urn, assets.ChannelRoleCall)
	if callChannel == nil {
		// can't start call, no channel that can call
		return nil, nil
	}

	hasCall := callChannel.HasRole(assets.ChannelRoleCall)
	if !hasCall {
		return nil, nil
	}

	channel := callChannel.Asset().(*models.Channel)
	call := models.NewOutgoingCall(oa.OrgID(), channel, contact, telURN.ID, trigger)
	if err := models.InsertCalls(ctx, rt.DB, []*models.Call{call}); err != nil {
		return nil, fmt.Errorf("error creating outgoing call: %w", err)
	}

	clog, err := RequestCallStart(ctx, rt, channel, telURNEncoded, call)

	// log any error inserting our channel log, but continue
	if clog != nil {
		if err := models.BulkWriterQueue(ctx, rt.Writers.Main, []*models.ChannelLog{clog}); err != nil {
			slog.Error("error writing channel log", "error", err)
		}
	}

	return call, err
}

func RequestCallStart(ctx context.Context, rt *runtime.Runtime, channel *models.Channel, telURN urns.URN, call *models.Call) (*models.ChannelLog, error) {
	// the domain that will be used for callbacks, can be specific for channels due to white labeling
	domain := channel.Config().GetString(models.ChannelConfigCallbackDomain, rt.Config.Domain)

	// get max concurrent calls if any
	maxCalls := channel.Config().GetInt(models.ChannelConfigMaxConcurrentCalls, 0)

	// max calls is set, lets see how many are currently active on this channel
	if maxCalls > 0 {
		count, err := models.ActiveCallCount(ctx, rt.DB, channel.ID())
		if err != nil {
			return nil, fmt.Errorf("error finding number of active calls: %w", err)
		}

		// we are at max calls, do not move on
		if count >= maxCalls {
			slog.Info("call being queued, max concurrent reached", "channel_id", channel.ID())
			err := call.SetThrottled(ctx, rt.DB)
			if err != nil {
				return nil, fmt.Errorf("error marking call as throttled: %w", err)
			}
			return nil, nil
		}
	}

	// create our callback
	params := &CallbackParams{Action: ActionStart, CallUUID: call.UUID()}

	resumeURL := fmt.Sprintf("https://%s/mr/ivr/c/%s/handle?%s", domain, channel.UUID(), params.Encode())
	statusURL := fmt.Sprintf("https://%s/mr/ivr/c/%s/status", domain, channel.UUID())

	// create the right service
	svc, err := GetService(channel)
	if err != nil {
		return nil, fmt.Errorf("unable to create IVR service: %w", err)
	}

	clog := models.NewChannelLog(models.ChannelLogTypeIVRStart, channel, svc.RedactValues(channel))
	defer clog.End()

	// try to request our call start
	callID, trace, err := svc.RequestCall(telURN, resumeURL, statusURL, channel.MachineDetection())
	if trace != nil {
		clog.HTTP(trace)
	}
	if err != nil {
		clog.Error(&clogs.Error{Message: err.Error()})

		// set our status as errored
		err := call.UpdateStatus(ctx, rt.DB, models.CallStatusFailed, 0, time.Now())
		if err != nil {
			return clog, fmt.Errorf("error setting errored status on session: %w", err)
		}
		return clog, nil
	}

	// update our channel session
	if err := call.UpdateExternalID(ctx, rt.DB, string(callID)); err != nil {
		return clog, fmt.Errorf("error updating session external id: %w", err)
	}
	if err := call.AttachLog(ctx, rt.DB, clog); err != nil {
		slog.Error("error attaching ivr channel log", "error", err)
	}

	return clog, nil
}

// HandleAsFailure marks the passed in call as errored and writes the appropriate error response to our writer
func HandleAsFailure(ctx context.Context, db *sqlx.DB, svc Service, call *models.Call, w http.ResponseWriter, rootErr error) error {
	err := call.SetFailed(ctx, db)
	if err != nil {
		slog.Error("error marking call as failed", "error", err)
	}
	return svc.WriteErrorResponse(w, rootErr)
}

// StartCall takes care of starting the given call
func StartCall(
	ctx context.Context, rt *runtime.Runtime, svc Service, resumeURL string, oa *models.OrgAssets,
	channel *models.Channel, call *models.Call, mc *models.Contact, urn urns.URN,
	r *http.Request, w http.ResponseWriter) error {

	// call isn't in a wired or in-progress status then we shouldn't be here
	if call.Status() != models.CallStatusWired && call.Status() != models.CallStatusInProgress {
		return HandleAsFailure(ctx, rt.DB, svc, call, w, fmt.Errorf("call in invalid state: %s", call.Status()))
	}

	// if we don't have a start then we must have a trigger so read that
	trigger, err := call.EngineTrigger(oa)
	if err != nil {
		return fmt.Errorf("error reading call trigger: %w", err)
	}

	f, err := oa.FlowByUUID(trigger.Flow().UUID)
	if err != nil {
		return fmt.Errorf("unable to load flow %s: %w", trigger.Flow().UUID, err)
	}
	flow := f.(*models.Flow)

	// check that call on service side is in the state we need to continue
	if errorReason := svc.CheckStartRequest(r); errorReason != "" {
		err := call.SetErrored(ctx, rt.DB, dates.Now(), flow.IVRRetryWait(), errorReason)
		if err != nil {
			return fmt.Errorf("error marking call as errored: %w", err)
		}

		errMsg := fmt.Sprintf("status updated: %s", call.Status())
		if call.Status() == models.CallStatusErrored {
			errMsg = fmt.Sprintf("%s, next_attempt: %s", errMsg, call.NextAttempt())
		}

		return svc.WriteErrorResponse(w, errors.New(errMsg))
	}

	// load contact and update on trigger to ensure we're not starting with outdated contact data
	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error loading flow contact: %w", err)
	}

	flowCall := flows.NewCall(call.UUID(), oa.SessionAssets().Channels().Get(channel.UUID()), urn.Identity())
	callEvt := events.NewCallCreated(flowCall)

	scene := runner.NewScene(mc, contact)
	scene.DBCall = call
	scene.Call = flowCall

	if err := scene.AddEvent(ctx, rt, oa, callEvt, models.NilUserID); err != nil {
		return fmt.Errorf("error adding call created event: %w", err)
	}

	if err := scene.StartSession(ctx, rt, oa, trigger, true); err != nil {
		return fmt.Errorf("error starting flow: %w", err)
	}
	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene: %w", err)
	}

	// have our service output our session status
	if err := svc.WriteSessionResponse(ctx, rt, oa, channel, scene, urn, resumeURL, r, w); err != nil {
		return fmt.Errorf("error writing ivr response for start: %w", err)
	}

	return nil
}

// ResumeCall takes care of resuming the given call
func ResumeCall(
	ctx context.Context, rt *runtime.Runtime,
	resumeURL string, svc Service,
	oa *models.OrgAssets, channel *models.Channel, call *models.Call, mc *models.Contact, urn urns.URN,
	r *http.Request, w http.ResponseWriter) error {

	// if call doesn't have an associated session then we shouldn't be here
	if call.SessionUUID() == "" {
		return HandleAsFailure(ctx, rt.DB, svc, call, w, errors.New("can't resume call without session"))
	}

	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating flow contact: %w", err)
	}

	session, err := models.GetWaitingSessionForContact(ctx, rt, oa, contact, call.SessionUUID())
	if err != nil {
		return fmt.Errorf("error loading session for contact #%d and call #%d: %w", mc.ID(), call.ID(), err)
	}

	if session == nil || session.SessionType != models.FlowTypeVoice {
		return HandleAsFailure(ctx, rt.DB, svc, call, w, fmt.Errorf("no active IVR session for contact"))
	}

	flow, err := oa.FlowByUUID(session.CurrentFlowUUID)
	if err != nil {
		return fmt.Errorf("unable to load flow %s: %w", session.CurrentFlowUUID, err)
	}

	// check if call has been marked as errored - it maybe have been updated by status callback
	if call.Status() == models.CallStatusErrored || call.Status() == models.CallStatusFailed {
		if err = models.ExitSessions(ctx, rt.DB, []flows.SessionUUID{session.UUID}, models.SessionStatusInterrupted); err != nil {
			slog.Error("error interrupting session for errored call", "error", err)
		}

		return svc.WriteErrorResponse(w, fmt.Errorf("ending call due to previous status callback"))
	}

	// preprocess this request
	body, err := svc.PreprocessResume(ctx, rt, call, r)
	if err != nil {
		return fmt.Errorf("error preprocessing resume: %w", err)
	}

	if body != nil {
		// guess our content type and set it
		contentType, _ := httpx.DetectContentType(body)
		w.Header().Set("Content-Type", contentType)
		_, err := w.Write(body)
		return err
	}

	// make sure our call is still happening
	status, _, _ := svc.StatusForRequest(r)
	if status != models.CallStatusInProgress {
		err := call.UpdateStatus(ctx, rt.DB, status, 0, time.Now())
		if err != nil {
			return fmt.Errorf("error updating status: %w", err)
		}
	}

	// get the input of our request
	ivrResume, err := svc.ResumeForRequest(r)
	if err != nil {
		return HandleAsFailure(ctx, rt.DB, svc, call, w, fmt.Errorf("error finding input for request: %w", err))
	}

	var msg *models.MsgInRef
	var resume flows.Resume
	var resumeEvent flows.Event
	var svcErr error
	switch res := ivrResume.(type) {
	case InputResume:
		msg, resume, svcErr, err = buildMsgResume(ctx, rt, oa, svc, channel, urn, call, flow.(*models.Flow), res)

		// TODO find a better way to model timeouts in IVR flows.. these should be timeout events not empty messages but
		// IVR flows don't have timeout routing
		if msg != nil {
			resumeEvent = resume.Event()
		}

	case DialResume:
		resume, svcErr, err = buildDialResume(res)
		resumeEvent = resume.Event()

	default:
		return fmt.Errorf("unknown resume type: %vvv", ivrResume)
	}

	if err != nil {
		return fmt.Errorf("error building resume for request: %w", err)
	}
	if svcErr != nil {
		return svc.WriteErrorResponse(w, svcErr)
	}
	if resume == nil {
		return svc.WriteErrorResponse(w, fmt.Errorf("no resume found, ending call"))
	}

	scene := runner.NewScene(mc, contact)
	scene.IncomingMsg = msg
	scene.DBCall = call
	scene.Call = flows.NewCall(call.UUID(), oa.SessionAssets().Channels().Get(channel.UUID()), urn.Identity())

	if resumeEvent != nil {
		if err := scene.AddEvent(ctx, rt, oa, resumeEvent, models.NilUserID); err != nil {
			return fmt.Errorf("error adding event: %w", err)
		}
	}

	if err := scene.ResumeSession(ctx, rt, oa, session, resume); err != nil {
		return fmt.Errorf("error resuming ivr flow: %w", err)
	}

	if err := scene.Commit(ctx, rt, oa); err != nil {
		return fmt.Errorf("error committing scene: %w", err)
	}

	// if still active, write out our response
	if status == models.CallStatusInProgress {
		err = svc.WriteSessionResponse(ctx, rt, oa, channel, scene, urn, resumeURL, r, w)
		if err != nil {
			return fmt.Errorf("error writing ivr response for resume: %w", err)
		}
	} else {
		err = models.ExitSessions(ctx, rt.DB, []flows.SessionUUID{session.UUID}, models.SessionStatusCompleted)
		if err != nil {
			slog.Error("error closing session", "error", err)
		}

		return svc.WriteErrorResponse(w, fmt.Errorf("call completed"))
	}

	return nil
}

// HandleStatus is called on status callbacks for an IVR call. We let the service decide whether the call has
// ended for some reason and update the state of the call and session if so
func HandleStatus(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, svc Service, call *models.Call, r *http.Request, w http.ResponseWriter) error {
	// read our status and duration from our service
	status, errorReason, duration := svc.StatusForRequest(r)

	if call.Status() == models.CallStatusErrored || call.Status() == models.CallStatusFailed {
		return svc.WriteEmptyResponse(w, fmt.Sprintf("status %s ignored, already errored", status))
	}

	// if we errored schedule a retry if appropriate
	if status == models.CallStatusErrored {

		// if this is an incoming call don't retry it so just fail permanently
		if call.Direction() == models.DirectionIn {
			call.SetFailed(ctx, rt.DB)
			return svc.WriteEmptyResponse(w, "no flow start found, status updated: F")
		}

		// get the associated flow from the trigger
		trigger, err := call.EngineTrigger(oa)
		if err != nil {
			return fmt.Errorf("unable to load call #%d trigger: %w", call.ID(), err)
		}

		fa, err := oa.FlowByUUID(trigger.Flow().UUID)
		if err != nil {
			return fmt.Errorf("unable to load flow %s: %w", trigger.Flow().UUID, err)
		}

		flow := fa.(*models.Flow)

		call.SetErrored(ctx, rt.DB, dates.Now(), flow.IVRRetryWait(), errorReason)

		if call.Status() == models.CallStatusErrored {
			return svc.WriteEmptyResponse(w, fmt.Sprintf("status updated: %s, next_attempt: %s", call.Status(), call.NextAttempt()))
		}

	} else if status == models.CallStatusFailed {
		call.SetFailed(ctx, rt.DB)
	} else {
		if status != call.Status() || duration > 0 {
			err := call.UpdateStatus(ctx, rt.DB, status, duration, time.Now())
			if err != nil {
				return fmt.Errorf("error updating call status: %w", err)
			}
		}
	}

	return svc.WriteEmptyResponse(w, fmt.Sprintf("status updated: %s", status))
}
