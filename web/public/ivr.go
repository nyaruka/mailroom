package public

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ivr"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternetRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/handle", newIVRHandler(handleCallback, models.ChannelLogTypeIVRCallback))
	web.InternetRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/status", newIVRHandler(handleStatus, models.ChannelLogTypeIVRStatus))
	web.InternetRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/incoming", newIVRHandler(handleIncoming, models.ChannelLogTypeIVRIncoming))

	// for providers that send every callback for a channel to one URL, and as a GET
	web.InternetRoute(http.MethodGet, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/callback", newIVRHandler(handleRouted, models.ChannelLogTypeIVRCallback))
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
				slog.Error("error attaching ivr channel log", "error", err, "http_request", loggable(r, svc.RedactValues(ch)))
			}
		}

		if err := recorder.End(); err != nil {
			slog.Error("error recording IVR request", "error", err, "http_request", loggable(r, svc.RedactValues(ch)))
		}

		clog.End()

		if _, err := rt.Dynamo.Main.Queue(clog); err != nil {
			slog.Error("error queuing IVR channel log to writer", "error", err, "elapsed", clog.Elapsed, "channel", ch.UUID())
		}

		return rerr
	}
}

func handleIncoming(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, r *http.Request, w http.ResponseWriter) (*models.Call, error) {
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
	mc, _, _, err := models.GetOrCreateContact(ctx, rt.DB, oa, userID, []urns.URN{urn}, ch.ID())
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get contact by urn: %w", err))
	}
	cu := mc.FindURN(urn)

	externalID, err := svc.CallIDForRequest(r)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get external id from request: %w", err))
	}

	call := models.NewIncomingCall(oa.OrgID(), ch, mc, cu.ID, externalID)
	if err := models.InsertCalls(ctx, rt.DB, []*models.Call{call}); err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("error inserting incoming call: %w", err))
	}

	// create an incoming call "task" and handle it to see if we have a trigger
	task := &ctasks.EventReceived{
		EventType: models.EventTypeIncomingCall,
		ChannelID: ch.ID(),
		URNID:     cu.ID,
		Extra:     nil,
	}
	scene, err := task.Handle(ctx, rt, oa, mc, call)
	if err != nil {
		slog.Error("error handling incoming call", "error", err, "http_request", loggable(r, svc.RedactValues(ch)))
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

	return handleCallAction(ctx, rt, oa, ch, svc, call, request.Action, r, w)
}

// handleRouted handles all callbacks for services whose provider sends them to a single URL for the channel
func handleRouted(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, r *http.Request, w http.ResponseWriter) (*models.Call, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*55)
	defer cancel()

	router, ok := svc.(ivr.CallbackRouter)
	if !ok {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("channel type %s doesn't support routed callbacks", ch.Type()))
	}

	action := router.CallbackAction(r)
	switch action {
	case ivr.ActionIncoming:
		call, err := handleIncoming(ctx, rt, oa, ch, svc, r, w)

		// a call that didn't start a flow has been told to hang up, and that's the last we need hear of it
		if call != nil && call.SessionUUID() == "" && call.Status() == models.CallStatusInProgress {
			if err := call.UpdateStatus(ctx, rt.DB, models.CallStatusCompleted, 0, time.Now()); err != nil {
				slog.Error("error completing incoming call", "error", err, "call", call.UUID())
			}
		}
		return call, err
	case ivr.ActionNone:
		// failing a call over a callback we don't understand would be worse than not acting on it
		slog.Warn("ignoring unrecognized IVR callback", "channel", ch.UUID(), "action", r.URL.Query().Get("action"))
		return nil, svc.WriteEmptyResponse(w, "unrecognized callback, ignoring")
	}

	call, err := findRoutedCall(ctx, rt, oa, ch, svc, router, action, r)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, svc.WriteEmptyResponse(w, "unknown call, ignoring")
	}
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to load call for request: %w", err))
	}

	switch action {
	case ivr.ActionStatus:
		// unlike starting or resuming, this doesn't need the call's contact, who may no longer be active
		if err := ivr.HandleStatus(ctx, rt, oa, svc, call, r, w); err != nil {
			slog.Error("error while handling status", "error", err, "http_request", loggable(r, svc.RedactValues(ch)))
			return call, ivr.HandleAsFailure(ctx, rt.DB, svc, call, w, err)
		}
		return call, nil

	case ivr.ActionStart:
		// a call that has ended on our side isn't brought back by a late or repeated start
		if call.Status() != models.CallStatusWired && call.Status() != models.CallStatusInProgress {
			return call, svc.WriteEmptyResponse(w, "call no longer active, ignoring")
		}

		// this is where we learn the provider's own id for a call we requested
		if externalID, _ := svc.CallIDForRequest(r); externalID != "" && externalID != call.ExternalID() && call.Status() == models.CallStatusWired {
			if err := call.UpdateExternalID(ctx, rt.DB, externalID); err != nil {
				return call, svc.WriteErrorResponse(w, fmt.Errorf("error updating call external id: %w", err))
			}
		}

	case ivr.ActionResume:
		// a call that has completed gets nothing more of a response that was being sent in parts
		if call.Status() == models.CallStatusCompleted {
			return call, svc.WriteEmptyResponse(w, "call already completed, ignoring")
		}
	}

	return handleCallAction(ctx, rt, oa, ch, svc, call, action, r, w)
}

// findRoutedCall finds the call that a request to a single callback URL is for, trying in turn our own UUID for the
// call, the provider's id for it and, for a call that the provider hasn't given us an id for yet, the URN called
func findRoutedCall(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, router ivr.CallbackRouter, action string, r *http.Request) (*models.Call, error) {
	// requests aren't signed so a UUID is only trusted to pick between this channel's own calls
	if callUUID := router.CallUUIDForRequest(r); uuids.Is(string(callUUID)) {
		call, err := models.GetCallByUUID(ctx, rt.DB, oa.OrgID(), callUUID)
		if err == nil && call.ChannelID() == ch.ID() {
			return call, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	if externalID, _ := svc.CallIDForRequest(r); externalID != "" {
		call, err := models.GetCallByExternalID(ctx, rt.DB, ch.ID(), externalID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}

		// a provider id that leads to a call which has ended can't be for one that is starting, so is a reused id
		if err == nil && (action != ivr.ActionStart || call.Status() == models.CallStatusWired || call.Status() == models.CallStatusInProgress) {
			return call, nil
		}
	}

	if action == ivr.ActionStart || action == ivr.ActionStatus {
		// a request that doesn't say who was called is one we can't match
		if urn, err := svc.URNForRequest(r); err == nil {
			return models.GetActiveOutgoingCallByURN(ctx, rt.DB, ch.ID(), urn)
		}
	}

	return nil, sql.ErrNoRows
}

// loggable returns a copy of the given request that can be logged, as its URL can carry secrets of the channel,
// which are the values to redact
func loggable(r *http.Request, redact []string) *http.Request {
	c := r.Clone(r.Context())
	for _, v := range redact {
		if v != "" {
			c.URL.RawQuery = strings.ReplaceAll(c.URL.RawQuery, v, "****")
			c.RequestURI = strings.ReplaceAll(c.RequestURI, v, "****")
		}
	}
	return c
}

// handleCallAction performs the given action on a call
func handleCallAction(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, call *models.Call, action string, r *http.Request, w http.ResponseWriter) (*models.Call, error) {
	// load our contact
	mc, err := models.LoadContact(ctx, rt.ReadonlyDB, oa, call.ContactID())
	if err != nil {
		return call, svc.WriteErrorResponse(w, fmt.Errorf("no such contact: %w", err))
	}
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
	switch action {
	case ivr.ActionStart:
		err = ivr.StartCall(ctx, rt, svc, resumeURL, oa, ch, call, mc, urn, r, w)
	case ivr.ActionResume:
		err = ivr.ResumeCall(ctx, rt, resumeURL, svc, oa, ch, call, mc, urn, r, w)
	default:
		err = svc.WriteErrorResponse(w, fmt.Errorf("unknown action: %s", action))
	}

	// had an error? mark our call as errored and log it
	if err != nil {
		slog.Error("error while handling IVR", "error", err, "http_request", loggable(r, svc.RedactValues(ch)))
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
		slog.Error("error while handling status", "error", err, "http_request", loggable(r, svc.RedactValues(ch)))
		return call, ivr.HandleAsFailure(ctx, rt.DB, svc, call, w, err)
	}

	return call, nil
}
