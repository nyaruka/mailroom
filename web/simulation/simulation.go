package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/assets/static"
	"github.com/nyaruka/goflow/excellent/tools"
	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

const (
	testURN         = urns.URN("tel:+12065551212")
	testChannelUUID = assets.ChannelUUID("440099cf-200c-4d45-a8e7-4a564f4a0e8b")
	testCallUUID    = flows.CallUUID("01979e0b-3072-7345-ae19-879750caaaf6")
)

func init() {
	web.InternalRoute(http.MethodPost, "/sim/start", web.JSONPayload(handleStart))
	web.InternalRoute(http.MethodPost, "/sim/resume", web.JSONPayload(handleResume))
}

type flowDefinition struct {
	UUID       assets.FlowUUID `json:"uuid"       validate:"required"`
	Definition json.RawMessage `json:"definition" validate:"required"`
}

type sessionRequest struct {
	OrgID  models.OrgID `json:"org_id"  validate:"required"`
	Assets struct {
		Channels []*static.Channel `json:"channels"`
	} `json:"assets"`
	Contact *flows.ContactEnvelope `json:"contact" validate:"required"`
	Call    *flows.CallEnvelope    `json:"call,omitempty"`
}

func (r *sessionRequest) channels() []assets.Channel {
	chs := make([]assets.Channel, len(r.Assets.Channels))
	for i := range r.Assets.Channels {
		chs[i] = r.Assets.Channels[i]
	}
	return chs
}

type simulationResponse struct {
	Session  flows.Session          `json:"session"`
	Contact  *flows.ContactEnvelope `json:"contact"`
	Events   []flows.Event          `json:"events"`
	Segments []flows.Segment        `json:"segments"`
	Context  *types.XObject         `json:"context,omitempty"`
}

func newSimulationResponse(session flows.Session, sprint flows.Sprint) *simulationResponse {
	var context *types.XObject
	if session != nil {
		context = session.CurrentContext()

		// include object defaults which are not marshaled by default, but not deprecated values
		if context != nil {
			tools.ContextWalkObjects(context, func(o *types.XObject) {
				o.SetMarshalOptions(true, false)
			})
		}
	}
	return &simulationResponse{
		Session:  session,
		Contact:  session.Contact().Marshal(),
		Events:   sprint.Events(),
		Segments: sprint.Segments(),
		Context:  context,
	}
}

// Starts a new engine session
//
//	{
//	  "org_id": 1,
//	  "contact": {"uuid": "468621a8-32e6-4cd2-afc1-04416f7151f0", "name": "Bob", ...},
//	  "trigger": {...},
//	  "assets": {...}
//	}
type startRequest struct {
	sessionRequest
	Trigger json.RawMessage `json:"trigger" validate:"required"`
}

// handleSimulationEvents takes care of updating our db with any events needed during simulation
func handleSimulationEvents(ctx context.Context, db models.DBorTx, oa *models.OrgAssets, es []flows.Event) error {
	// nicpottier: this could be refactored into something more similar to how we handle normal events (ie hooks) if
	// we see ourselves taking actions for more than just webhook events
	wes := make([]*models.WebhookEvent, 0)
	for _, e := range es {
		if e.Type() == events.TypeResthookCalled {
			rec := e.(*events.ResthookCalled)
			resthook := oa.ResthookBySlug(rec.Resthook)
			if resthook != nil {
				we := models.NewWebhookEvent(oa.OrgID(), resthook.ID(), string(rec.Payload), rec.CreatedOn())
				wes = append(wes, we)
			}
		}
	}

	// noop in the case of no events
	return models.InsertWebhookEvents(ctx, db, wes)
}

// handles a request to /start
func handleStart(ctx context.Context, rt *runtime.Runtime, r *startRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("error loading org assets: %w", err)
	}

	// create clone of assets for simulation
	oa, err = oa.CloneForSimulation(ctx, rt, r.channels())
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("unable to clone org: %w", err)
	}

	contact, err := r.Contact.Unmarshal(oa.SessionAssets(), assets.IgnoreMissing)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("unable to read contact: %w", err)
	}

	var call *flows.Call
	if r.Call != nil {
		call = r.Call.Unmarshal(oa.SessionAssets(), assets.IgnoreMissing)
	}

	trigger, err := triggers.Read(oa.SessionAssets(), r.Trigger, assets.IgnoreMissing)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("unable to read trigger: %w", err)
	}

	return triggerFlow(ctx, rt, oa, contact, call, trigger)
}

// triggerFlow creates a new session with the passed in trigger, returning our standard response
func triggerFlow(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contact *flows.Contact, call *flows.Call, trigger flows.Trigger) (any, int, error) {
	// start our flow session
	session, sprint, err := goflow.Simulator(ctx, rt).NewSession(ctx, oa.SessionAssets(), oa.Env(), contact, trigger, call)
	if err != nil {
		return nil, 0, fmt.Errorf("error starting session: %w", err)
	}

	err = handleSimulationEvents(ctx, rt.DB, oa, sprint.Events())
	if err != nil {
		return nil, 0, fmt.Errorf("error handling simulation events: %w", err)
	}

	return newSimulationResponse(session, sprint), http.StatusOK, nil
}

// Resumes an existing engine session
//
//	{
//	  "org_id": 1,
//	  "contact": {"uuid": "468621a8-32e6-4cd2-afc1-04416f7151f0", "name": "Bob", ...},
//	  "session": {"uuid": "01979d37-9fe7-7e16-8cc0-bae91a66cfe1", "runs": [...], ...},
//	  "resume": {...},
//	  "assets": {...}
//	}
type resumeRequest struct {
	sessionRequest

	Session json.RawMessage `json:"session" validate:"required"`
	Resume  json.RawMessage `json:"resume" validate:"required"`
}

func handleResume(ctx context.Context, rt *runtime.Runtime, r *resumeRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("error loading org assets: %w", err)
	}

	// create clone of assets for simulation
	oa, err = oa.CloneForSimulation(ctx, rt, r.channels())
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	contact, err := r.Contact.Unmarshal(oa.SessionAssets(), assets.IgnoreMissing)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("unable to read contact: %w", err)
	}

	var call *flows.Call
	if r.Call != nil {
		call = r.Call.Unmarshal(oa.SessionAssets(), assets.IgnoreMissing)
	}

	resume, err := resumes.Read(oa.SessionAssets(), r.Resume, assets.IgnoreMissing)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	session, err := goflow.Simulator(ctx, rt).ReadSession(oa.SessionAssets(), r.Session, oa.Env(), contact, call, assets.IgnoreMissing)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	// if this is a msg resume we want to check whether it might be caught by a trigger
	if resume.Type() == resumes.TypeMsg {
		msgResume := resume.(*resumes.Msg)
		msgEvt := msgResume.Event().(*events.MsgReceived)

		trigger, keyword := models.FindMatchingMsgTrigger(oa, nil, contact, msgEvt.Msg.Text())
		if trigger != nil {
			var flow *models.Flow
			for _, r := range session.Runs() {
				if r.Status() == flows.RunStatusWaiting {
					f, _ := oa.FlowByUUID(r.FlowReference().UUID)
					if f != nil {
						flow = f.(*models.Flow)
					}
					break
				}
			}

			// we don't have a current flow or the current flow doesn't ignore triggers
			if flow == nil || (!flow.IgnoreTriggers() && trigger.TriggerType() == models.KeywordTriggerType) {
				triggeredFlow, err := oa.FlowByID(trigger.FlowID())
				if err != nil && err != models.ErrNotFound {
					return nil, 0, fmt.Errorf("unable to load triggered flow: %w", err)
				}

				if triggeredFlow != nil {
					tb := triggers.NewBuilder(triggeredFlow.Reference())

					var sessionTrigger flows.Trigger
					var call *flows.Call
					if triggeredFlow.FlowType() == models.FlowTypeVoice {
						sessionTrigger = tb.MsgReceived(msgEvt).Build()
						call = flows.NewCall(testCallUUID, oa.SessionAssets().Channels().Get(testChannelUUID), testURN)
					} else {
						mtb := tb.MsgReceived(msgEvt)
						if keyword != "" {
							mtb = mtb.WithMatch(&triggers.KeywordMatch{Type: trigger.KeywordMatchType(), Keyword: keyword})
						}
						sessionTrigger = mtb.Build()
					}

					return triggerFlow(ctx, rt, oa, contact, call, sessionTrigger)
				}
			}
		}
	}

	// if our session is already complete, then this is a no-op, return the session unchanged
	if session.Status() != flows.SessionStatusWaiting {
		return &simulationResponse{Session: session, Events: nil}, http.StatusOK, nil
	}

	// resume our session
	sprint, err := session.Resume(ctx, resume)
	if err != nil {
		return nil, 0, err
	}

	err = handleSimulationEvents(ctx, rt.DB, oa, sprint.Events())
	if err != nil {
		return nil, 0, fmt.Errorf("error handling simulation events: %w", err)
	}

	return newSimulationResponse(session, sprint), http.StatusOK, nil
}
