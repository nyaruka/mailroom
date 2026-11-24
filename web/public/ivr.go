package public

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ivr"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner/clocks"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

// how long an IVR request waits for the contact lock, short enough to leave time to respond before the provider gives up
var contactLockWait = 5 * time.Second

func init() {
	web.InternetRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/handle", newIVRHandler(handleCallback, models.ChannelLogTypeIVRCallback))
	web.InternetRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/status", newIVRHandler(handleStatus, models.ChannelLogTypeIVRStatus))
	web.InternetRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/incoming", newIVRHandler(handleIncoming, models.ChannelLogTypeIVRIncoming))
}

type ivrHandlerFn func(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, r *http.Request, w http.ResponseWriter) (*models.Call, error)

func newIVRHandler(handler ivrHandlerFn, logType svclogs.Type) web.Handler {
	return func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
		channelUUID := assets.ChannelUUID(r.PathValue("uuid"))

		// load the org id for this UUID (we could load the entire channel here but we want to take the same paths through everything else)
		orgID, err := models.OrgIDForChannelUUID(ctx, rt.DB, channelUUID)
		if err != nil {
			return writeGenericErrorResponse(w, err)
		}

		// load our org assets
		oa, err := models.GetOrgAssets(ctx, rt, orgID)
		if err != nil {
			return writeGenericErrorResponse(w, fmt.Errorf("error loading org assets: %w", err))
		}

		// and our channel
		ch := oa.ChannelByUUID(channelUUID)
		if ch == nil {
			return writeGenericErrorResponse(w, fmt.Errorf("no active channel with uuid: %s: %w", channelUUID, err))
		}

		// get the IVR service for this channel
		svc, err := ivr.GetService(rt.HTTP.Services, ch)
		if svc == nil {
			return writeGenericErrorResponse(w, fmt.Errorf("unable to get service for channel: %s: %w", ch.UUID(), err))
		}

		recorder, err := httpx.NewRecorder(r, w, true)
		if err != nil {
			return svc.WriteErrorResponse(w, fmt.Errorf("error reading request body: %w", err))
		}

		// validate this request's signature
		err = svc.ValidateRequestSignature(r)
		if err != nil {
			return svc.WriteErrorResponse(w, fmt.Errorf("request failed signature validation: %w", err))
		}

		clog := models.NewChannelLogForIncoming(logType, ch, recorder, svc.RedactValues(ch))

		call, rerr := handler(ctx, rt, oa, ch, svc, r, recorder.ResponseWriter)
		if call != nil {
			if err := call.AttachLog(ctx, rt.DB, clog); err != nil {
				slog.Error("error attaching ivr channel log", "error", err, "http_request", r)
			}
		}

		if err := recorder.End(); err != nil {
			slog.Error("error recording IVR request", "error", err, "http_request", r)
		}

		clog.End()

		if _, err := rt.Dynamo.Main.Queue(clog); err != nil {
			slog.Error("error queuing IVR channel log to writer", "error", err, "elapsed", clog.Elapsed, "channel", ch.UUID())
		}

		return rerr
	}
}

func handleIncoming(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, r *http.Request, w http.ResponseWriter) (*models.Call, error) {
	if oa.Org().IsSuspended() {
		return nil, svc.WriteRejectResponse(w)
	}

	// lookup the URN of the caller
	urn, err := svc.URNForRequest(r)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to find URN in request: %w", err))
	}

	userID, err := models.GetSystemUserID(ctx, rt.DB.DB)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get system user id: %w", err))
	}

	// get the contact for this URN
	mc, _, _, err := models.GetOrCreateContact(ctx, rt.DB, oa, userID, models.ContactSourceInbound, []urns.URN{urn}, ch.ID())
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get contact by urn: %w", err))
	}

	mc, unlock, err := lockAndLoadContact(ctx, rt, oa, mc.ID())
	if err != nil {
		return nil, svc.WriteErrorResponse(w, err)
	}
	defer unlock()

	cu := mc.FindURN(urn)

	externalID, err := svc.CallIDForRequest(r)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get external id from request: %w", err))
	}

	call := models.NewIncomingCall(oa.OrgID(), ch, mc, cu.ID, externalID)
	if err := models.InsertCalls(ctx, rt.DB, []*models.Call{call}); err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("error inserting incoming call: %w", err))
	}

	models.IncrementCallTallies(ctx, rt, oa.OrgID(), []*models.Call{call})

	// create an incoming call "task" and handle it to see if we have a trigger
	task := &ctasks.EventReceived{
		EventType: models.EventTypeIncomingCall,
		ChannelID: ch.ID(),
		URNID:     cu.ID,
		Extra:     nil,
	}
	scene, err := task.Handle(ctx, rt, oa, mc, call)
	if err != nil {
		slog.Error("error handling incoming call", "error", err, "http_request", r)
		return call, svc.WriteErrorResponse(w, fmt.Errorf("error handling incoming call: %w", err))
	}

	// if we matched with an incoming-call trigger, we'll have a session
	if scene != nil && scene.Session != nil {
		// that might have started a non-voice flow, in which case we need to reject this call
		if scene.Session.Type() != flows.FlowTypeVoice {
			return call, svc.WriteRejectResponse(w)
		}

		// build our resume URL
		resumeURL := buildResumeURL(rt.Config, ch, call)

		// have our client output our session status
		err = svc.WriteSessionResponse(ctx, rt, oa, ch, scene, urn, resumeURL, r, w)
		if err != nil {
			return call, fmt.Errorf("error writing ivr response for start: %w", err)
		}

		return call, nil
	}

	// write our empty response
	return call, svc.WriteEmptyResponse(w, "missed call handled")
}

// lockAndLoadContact locks the given contact, as every other path that starts or resumes sessions does, so that the
// sessions changed by this request can't race with theirs. Otherwise a session started concurrently elsewhere can lose
// its fires and stop being the contact's current session, leaving it waiting forever. The contact is loaded from the
// primary database because a replica may not yet reflect the changes of the previous lock holder.
func lockAndLoadContact(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactID models.ContactID) (*models.Contact, func(), error) {
	locks, _, err := clocks.TryToLock(ctx, rt, oa, []models.ContactID{contactID}, contactLockWait)
	if err != nil {
		return nil, nil, fmt.Errorf("error locking contact: %w", err)
	}
	if len(locks) == 0 {
		return nil, nil, fmt.Errorf("timed out waiting for lock on contact #%d", contactID)
	}

	unlock := func() {
		if err := clocks.Unlock(context.Background(), rt, oa, locks); err != nil {
			slog.Error("error unlocking contact", "error", err, "contact", contactID)
		}
	}

	mc, err := models.LoadContact(ctx, rt.DB, oa, contactID)
	if err != nil {
		unlock()
		return nil, nil, fmt.Errorf("no such contact: %w", err)
	}

	return mc, unlock, nil
}

// writeGenericErrorResponse is just a small utility method to write out a simple JSON error when we don't have a client yet
func writeGenericErrorResponse(w http.ResponseWriter, err error) error {
	return web.WriteMarshalled(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func buildResumeURL(cfg *runtime.Config, channel *models.Channel, call *models.Call) string {
	domain := channel.Config().GetString(models.ChannelConfigCallbackDomain, cfg.Domain)
	params := &ivr.CallbackParams{Action: ivr.ActionResume, CallUUID: call.UUID()}

	return fmt.Sprintf("https://%s/mr/ivr/c/%s/handle?%s", domain, channel.UUID(), params.Encode())
}

// handles all incoming IVR requests related to a flow (status is handled elsewhere)
func handleCallback(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, r *http.Request, w http.ResponseWriter) (*models.Call, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*55)
	defer cancel()

	request := &ivr.CallbackParams{}
	if err := web.DecodeAndValidateForm(request, r); err != nil {
		return nil, fmt.Errorf("IVR callback request failed validation: %w", err)
	}

	// load our call
	call, err := models.GetCallByUUID(ctx, rt.DB, oa.OrgID(), request.CallUUID)
	if err != nil {
		return nil, fmt.Errorf("unable to load call with UUID %s: %w", request.CallUUID, err)
	}

	// status updates don't lock the contact, so that a resume holding the lock can't delay our response
	if request.Action == ivr.ActionStatus {
		if err := ivr.HandleStatus(ctx, rt, oa, svc, call, r, w); err != nil {
			slog.Error("error while handling IVR", "error", err, "http_request", r)
			return call, ivr.HandleAsFailure(ctx, rt.DB, svc, call, w, err)
		}
		interruptHungUpSession(ctx, rt, oa, call)
		return call, nil
	}

	mc, unlock, err := lockAndLoadContact(ctx, rt, oa, call.ContactID())
	if err != nil {
		return call, ivr.HandleAsFailure(ctx, rt.DB, svc, call, w, err)
	}
	defer unlock()

	if mc.Status() != models.ContactStatusActive {
		return call, svc.WriteErrorResponse(w, fmt.Errorf("no contact with id: %d", call.ContactID()))
	}

	// load the URN for this call
	cu, err := models.LoadContactURN(ctx, rt.DB, call.ContactURNID())
	if err != nil {
		return call, svc.WriteErrorResponse(w, fmt.Errorf("unable to find call urn: %d", call.ContactURNID()))
	}

	urn, _ := cu.Encode(oa)

	// make sure our URN is indeed present on our contact, no funny business
	found := false
	for _, u := range mc.URNs() {
		if u.ID == cu.ID {
			found = true
		}
	}
	if !found {
		return call, svc.WriteErrorResponse(w, fmt.Errorf("unable to find URN: %s on contact: %d", urn, call.ContactID()))
	}

	resumeURL := buildResumeURL(rt.Config, ch, call)

	// if this a start, start our contact
	switch request.Action {
	case ivr.ActionStart:
		err = ivr.StartCall(ctx, rt, svc, resumeURL, oa, ch, call, mc, urn, r, w)
	case ivr.ActionResume:
		err = ivr.ResumeCall(ctx, rt, resumeURL, svc, oa, ch, call, mc, urn, r, w)
	default:
		err = svc.WriteErrorResponse(w, fmt.Errorf("unknown action: %s", request.Action))
	}

	// had an error? mark our call as errored and log it
	if err != nil {
		slog.Error("error while handling IVR", "error", err, "http_request", r)
		return call, ivr.HandleAsFailure(ctx, rt.DB, svc, call, w, err)
	}

	return call, nil
}

// handleStatus handles all incoming IVR events / status updates
func handleStatus(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, r *http.Request, w http.ResponseWriter) (*models.Call, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*55)
	defer cancel()

	// preprocess this status
	body, err := svc.PreprocessStatus(ctx, rt, r)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("error while preprocessing status: %w", err))
	}
	if len(body) > 0 {
		contentType, _ := httpx.DetectContentType(body)
		w.Header().Set("Content-Type", contentType)
		_, err := w.Write(body)
		return nil, err
	}

	// get our external id
	externalID, err := svc.CallIDForRequest(r)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get call id for request: %w", err))
	}

	// load our call
	call, err := models.GetCallByExternalID(ctx, rt.DB, ch.ID(), externalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, svc.WriteEmptyResponse(w, "unknown call, ignoring")
	}
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to load call with id: %s: %w", externalID, err))
	}

	err = ivr.HandleStatus(ctx, rt, oa, svc, call, r, w)

	// had an error? mark our call as errored and log it
	if err != nil {
		slog.Error("error while handling status", "error", err, "http_request", r)
		return call, ivr.HandleAsFailure(ctx, rt.DB, svc, call, w, err)
	}

	interruptHungUpSession(ctx, rt, oa, call)

	return call, nil
}

// interruptHungUpSession queues interruption of the call's session if the call has completed while that session is
// still waiting, i.e. the contact hung up mid-flow. Otherwise the session would wait until it expires. Failures are only
// logged because the expiry will still end the session.
func interruptHungUpSession(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, call *models.Call) {
	if call.Status() != models.CallStatusCompleted || call.SessionUUID() == "" {
		return
	}

	// most calls complete after their flow has ended, so check cheaply before queueing anything
	current, err := models.IsCurrentSession(ctx, rt.DB, call.ContactID(), call.SessionUUID())
	if err != nil {
		slog.Error("error checking session of completed call", "error", err, "call", call.UUID())
		return
	}
	if !current {
		return
	}

	task := &tasks.InterruptSessionBatch{
		Sessions: []models.SessionRef{{UUID: call.SessionUUID(), ContactID: call.ContactID()}},
		Status:   flows.SessionStatusInterrupted,
	}
	if err := tasks.Queue(ctx, rt, rt.Queues.Realtime, oa.OrgID(), task, false); err != nil {
		slog.Error("error queueing interrupt of session for completed call", "error", err, "call", call.UUID())
	}
}
