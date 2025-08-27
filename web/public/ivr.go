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
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/clogs"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.PublicRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/handle", newIVRHandler(handleCallback, models.ChannelLogTypeIVRCallback))
	web.PublicRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/status", newIVRHandler(handleStatus, models.ChannelLogTypeIVRStatus))
	web.PublicRoute(http.MethodPost, "/ivr/c/{uuid:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/incoming", newIVRHandler(handleIncoming, models.ChannelLogTypeIVRIncoming))
}

type ivrHandlerFn func(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, ch *models.Channel, svc ivr.Service, r *http.Request, w http.ResponseWriter) (*models.Call, error)

func newIVRHandler(handler ivrHandlerFn, logType clogs.Type) web.Handler {
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
		svc, err := ivr.GetService(ch)
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

		if err := models.BulkWriterQueue(ctx, rt.Writers.Main, []*models.ChannelLog{clog}); err != nil {
			slog.Error("error writing ivr channel log", "error", err, "elapsed", clog.Elapsed, "channel", ch.UUID())
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
	contact, _, _, err := models.GetOrCreateContact(ctx, rt.DB, oa, userID, []urns.URN{urn}, ch.ID())
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get contact by urn: %w", err))
	}

	urn, err = models.URNForURN(ctx, rt.DB, oa, urn)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to load urn: %w", err))
	}

	urnID := models.GetURNID(urn)
	if urnID == models.NilURNID {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get id for URN: %w", err))
	}

	externalID, err := svc.CallIDForRequest(r)
	if err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("unable to get external id from request: %w", err))
	}

	call := models.NewIncomingCall(oa.OrgID(), ch, contact, urnID, externalID)
	if err := models.InsertCalls(ctx, rt.DB, []*models.Call{call}); err != nil {
		return nil, svc.WriteErrorResponse(w, fmt.Errorf("error inserting incoming call: %w", err))
	}

	// create an incoming call "task" and handle it to see if we have a trigger
	task := &ctasks.EventReceivedTask{
		EventType: models.EventTypeIncomingCall,
		ChannelID: ch.ID(),
		URNID:     urnID,
		Extra:     nil,
		CreatedOn: time.Now(),
	}
	scene, err := task.Handle(ctx, rt, oa, contact, call)
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

	// load our contact
	contact, err := models.LoadContact(ctx, rt.ReadonlyDB, oa, call.ContactID())
	if err != nil {
		return call, svc.WriteErrorResponse(w, fmt.Errorf("no such contact: %w", err))
	}
	if contact.Status() != models.ContactStatusActive {
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
	for _, u := range contact.URNs() {
		if u.Identity() == urn.Identity() {
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
		err = ivr.StartCall(ctx, rt, svc, resumeURL, oa, ch, call, contact, urn, r, w)
	case ivr.ActionResume:
		err = ivr.ResumeCall(ctx, rt, resumeURL, svc, oa, ch, call, contact, urn, r, w)
	case ivr.ActionStatus:
		err = ivr.HandleStatus(ctx, rt, oa, svc, call, r, w)

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

	return call, nil
}
