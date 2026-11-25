package mnotify

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/core/hints"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/v26/core/ivr"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	mrutils "github.com/nyaruka/mailroom/v26/utils"
)

const (
	mnotifyChannelType = models.ChannelType("MN")

	apiKeyConfig = "api_key"
	secretConfig = "secret"

	gatherTimeout   = 30  // seconds to wait for digits
	recordTimeout   = 600 // max seconds of a recording
	maxGatherDigits = 32  // digits to collect when the flow doesn't want a fixed number

	defaultFinishKey = "#" // ends digit collection when the flow wants neither a fixed number nor a key

	// how long the actions of a sprint that are still to be sent stay queued
	queueTTL = 60 * 60

	// callback parameters, which are matched ignoring case and underscores as the provider isn't consistent in how
	// it writes them, e.g. CallerId, callerId and caller_id
	paramCallerID     = "callerid"
	paramExtension    = "extension"
	paramSequenceID   = "sequenceid"
	paramIsStarting   = "isstarting"
	paramDTMF         = "dtmf"
	paramAction       = "action"
	paramContext      = "context"
	paramRecordingURL = "recordingurl"
	paramToken        = "token"

	// the tracking id we gave when starting a call. Whether callbacks carry it isn't in the provider's published
	// reference.
	paramUniqueID = "uniqueid"

	// the outcome of a call that has ended. These aren't in the provider's published reference, so are assumptions
	// until we've seen real callbacks.
	paramDisposition = "disposition"
	paramDuration    = "duration"

	actionPlayback   = "playback"
	actionGetDigits  = "get_digits"
	actionBackground = "background"
	actionRecord     = "record"
	actionHangup     = "hangup"

	contextOutgoing = "outgoing"
)

// params are the parameters of a callback, keyed by name without case or underscores
type params map[string]string

func requestParams(r *http.Request) params {
	ps := make(params)
	for name, values := range r.URL.Query() {
		if len(values) > 0 {
			ps[strings.ToLower(strings.ReplaceAll(name, "_", ""))] = values[0]
		}
	}
	return ps
}

func (p params) Get(name string) string { return p[name] }

type service struct {
	httpClient *http.Client
	channel    *models.Channel
	apiKey     string
	secret     string
}

func init() {
	ivr.RegisterService(mnotifyChannelType, NewServiceFromChannel)
}

// NewServiceFromChannel creates a new mNotify IVR service for the passed in channel
func NewServiceFromChannel(httpClient *http.Client, channel *models.Channel) (ivr.Service, error) {
	apiKey := channel.Config().GetString(apiKeyConfig, "")
	secret := channel.Config().GetString(secretConfig, "")
	if apiKey == "" || secret == "" {
		return nil, fmt.Errorf("missing api_key or secret on channel config for channel: %s", channel.UUID())
	}

	return &service{httpClient: httpClient, channel: channel, apiKey: apiKey, secret: secret}, nil
}

// RequestCall implements ivr.Service. The provider doesn't take callback URLs per call - all callbacks for a
// channel go to the one URL configured for its number - so we give it our call UUID as its tracking id.
func (s *service) RequestCall(number urns.URN, handleURL string, statusURL string, machineDetection bool) (ivr.CallID, *httpx.Trace, error) {
	callUUID := ""
	if u, err := url.Parse(handleURL); err == nil {
		callUUID = u.Query().Get("call")
	}
	if callUUID == "" {
		return ivr.NilCallID, nil, errors.New("unable to find call UUID in handle URL")
	}

	body := &CallRequest{
		FromNumber: strings.TrimPrefix(s.channel.Address(), "+"),
		ToNumber:   strings.TrimPrefix(number.Path(), "+"),
		UUID:       string(s.channel.UUID()),
		UniqueID:   callUUID,
		Context:    contextOutgoing,
	}

	sendURL := BaseURL + callPath + "?key=" + url.QueryEscape(s.apiKey)
	req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(jsonx.MustMarshal(body)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	trace, _, err := mrutils.DoTraced(s.httpClient, req)
	if err != nil {
		return ivr.NilCallID, trace, fmt.Errorf("error trying to start call: %w", err)
	}

	if trace.Response.StatusCode/100 != 2 {
		return ivr.NilCallID, trace, fmt.Errorf("received non 2XX status for call start: %d", trace.Response.StatusCode)
	}

	resp := &CallResponse{}
	if err := json.Unmarshal(trace.ResponseBody, resp); err != nil {
		return ivr.NilCallID, trace, fmt.Errorf("unable to read call start response: %w", err)
	}
	if !resp.Success {
		return ivr.NilCallID, trace, fmt.Errorf("call start was not successful: %s", resp.Message)
	}

	// until the call is answered and we learn the provider's own id for it, our UUID is the only id it has
	return ivr.CallID(callUUID), trace, nil
}

// HangupCall implements ivr.Service. There is no API to end a call, it ends on the next callback we answer.
func (s *service) HangupCall(externalID string) (*httpx.Trace, error) {
	return nil, nil
}

// CallbackAction implements ivr.CallbackRouter
func (s *service) CallbackAction(r *http.Request) string {
	q := requestParams(r)

	if q.Get(paramDisposition) != "" || q.Get(paramAction) == actionHangup {
		return ivr.ActionStatus
	}
	if q.Get(paramDTMF) != "" {
		// digits are an answer to something we asked for, whatever else the callback says
		return ivr.ActionResume
	}
	if isTrue(q.Get(paramIsStarting)) {
		// the context of a call we started is one we chose, that of any other call is set on the provider side
		if strings.EqualFold(q.Get(paramContext), contextOutgoing) || s.CallUUIDForRequest(r) != "" {
			return ivr.ActionStart
		}
		return ivr.ActionIncoming
	}

	switch q.Get(paramAction) {
	case actionPlayback, actionGetDigits, actionBackground, actionRecord:
		return ivr.ActionResume
	}

	// anything else isn't the end of something we asked for, so isn't something to act on
	return ivr.ActionNone
}

// CallUUIDForRequest implements ivr.CallbackRouter
func (s *service) CallUUIDForRequest(r *http.Request) core.CallUUID {
	return core.CallUUID(requestParams(r).Get(paramUniqueID))
}

// StatusForRequest implements ivr.Service
func (s *service) StatusForRequest(r *http.Request) (models.CallStatus, models.CallError, int) {
	q := requestParams(r)
	duration, _ := strconv.Atoi(q.Get(paramDuration))

	switch strings.ToUpper(strings.TrimSpace(q.Get(paramDisposition))) {
	case "ANSWERED":
		return models.CallStatusCompleted, "", duration
	case "NO ANSWER", "NOANSWER", "NO_ANSWER":
		return models.CallStatusErrored, models.CallErrorNoAnswer, 0
	case "BUSY":
		return models.CallStatusErrored, models.CallErrorBusy, 0
	case "FAILED":
		return models.CallStatusErrored, models.CallErrorProvider, 0
	case "":
		if q.Get(paramAction) == actionHangup {
			return models.CallStatusCompleted, "", duration
		}
		return models.CallStatusInProgress, "", 0
	default:
		slog.Error("unknown call disposition in status callback", "disposition", q.Get(paramDisposition))
		return models.CallStatusFailed, models.CallErrorProvider, 0
	}
}

// ResumeForRequest implements ivr.Service
func (s *service) ResumeForRequest(r *http.Request) (ivr.Resume, error) {
	q := requestParams(r)

	if q.Get(paramDTMF) != "" {
		return ivr.InputResume{Input: q.Get(paramDTMF)}, nil
	}

	switch q.Get(paramAction) {
	case actionGetDigits, actionBackground:
		// no digits is a timeout, which is an empty input
		return ivr.InputResume{}, nil

	case actionRecord:
		recordingURL := q.Get(paramRecordingURL)
		if recordingURL == "" {
			return ivr.InputResume{}, nil
		}
		return ivr.InputResume{Attachment: utils.Attachment("audio:" + recordingURL)}, nil

	default:
		return nil, fmt.Errorf("unable to resume for action: %s", q.Get(paramAction))
	}
}

// CheckStartRequest implements ivr.Service
func (s *service) CheckStartRequest(r *http.Request) models.CallError {
	return ""
}

// PreprocessResume implements ivr.Service. If this callback is the end of a playback, then there are more actions
// from the last sprint queued and the next one is our response rather than anything from the flow.
func (s *service) PreprocessResume(ctx context.Context, rt *runtime.Runtime, call *models.Call, r *http.Request) ([]byte, error) {
	if requestParams(r).Get(paramAction) != actionPlayback {
		return nil, nil
	}

	vc := rt.VK.Get()
	defer vc.Close()

	// this takes each end of a playback to be reported once - one that was reported again would get the action
	// after the one it should
	next, err := valkey.Bytes(valkey.DoContext(vc, ctx, "LPOP", queueKey(call.UUID())))
	if err != nil && err != valkey.ErrNil {
		return nil, fmt.Errorf("error popping queued action: %w", err)
	}
	if len(next) == 0 {
		// a sprint always ends in an action that isn't a playback, so this is a queue that has expired
		return jsonx.MustMarshal(hangupResponse("no more actions for call")), nil
	}

	return next, nil
}

// PreprocessStatus implements ivr.Service
func (s *service) PreprocessStatus(ctx context.Context, rt *runtime.Runtime, r *http.Request) ([]byte, error) {
	return nil, nil
}

// ValidateRequestSignature implements ivr.Service. Callbacks aren't signed, so instead the callback URL configured
// for the channel's number carries a secret.
func (s *service) ValidateRequestSignature(r *http.Request) error {
	if subtle.ConstantTimeCompare([]byte(requestParams(r).Get(paramToken)), []byte(s.secret)) != 1 {
		return errors.New("invalid token")
	}
	return nil
}

// DownloadMedia implements ivr.Service
func (s *service) DownloadMedia(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return s.httpClient.Do(req)
}

// URNForRequest implements ivr.Service. The contact is whichever party isn't the channel's own number.
func (s *service) URNForRequest(r *http.Request) (urns.URN, error) {
	q := requestParams(r)

	number := q.Get(paramCallerID)
	if number == "" || sameNumber(number, s.channel.Address()) {
		number = q.Get(paramExtension)
	}
	if number == "" {
		return "", errors.New("no caller number found in request")
	}

	urn, err := urns.ParsePhone(number, s.channel.Country(), true, false)
	if err != nil {
		// numbers can come in international format without a leading plus
		urn, err = urns.ParsePhone("+"+number, s.channel.Country(), true, false)
	}
	return urn, err
}

// CallIDForRequest implements ivr.Service
func (s *service) CallIDForRequest(r *http.Request) (string, error) {
	id := requestParams(r).Get(paramSequenceID)
	if id == "" {
		return "", errors.New("no sequence id set on call")
	}
	return id, nil
}

// RedactValues implements ivr.Service
func (s *service) RedactValues(ch *models.Channel) []string {
	return []string{ch.Config().GetString(apiKeyConfig, ""), ch.Config().GetString(secretConfig, "")}
}

// WriteSessionResponse implements ivr.Service. Only the first action of the sprint is written, the rest are queued
// and written one at a time as the provider calls back to say each playback has ended.
func (s *service) WriteSessionResponse(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.Channel, scene *runner.Scene, number urns.URN, resumeURL string, req *http.Request, w http.ResponseWriter) error {
	if scene.Session.Status() == flows.SessionStatusFailed {
		return fmt.Errorf("cannot write IVR response for failed session")
	}

	actions, err := ActionsForSprint(rt.Config, scene.Sprint.Events())
	if err != nil {
		return fmt.Errorf("unable to build response for IVR call: %w", err)
	}

	vc := rt.VK.Get()
	defer vc.Close()

	key := queueKey(scene.DBCall.UUID())
	vc.Send("MULTI")
	vc.Send("DEL", key)
	for _, a := range actions[1:] {
		vc.Send("RPUSH", key, jsonx.MustMarshal(&Response{Actions: a}))
	}
	vc.Send("EXPIRE", key, queueTTL)
	if _, err := valkey.DoContext(vc, ctx, "EXEC"); err != nil {
		return fmt.Errorf("error queuing actions: %w", err)
	}

	return s.writeResponse(w, &Response{Actions: actions[0]})
}

// WriteRejectResponse implements ivr.Service
func (s *service) WriteRejectResponse(w http.ResponseWriter) error {
	return s.writeResponse(w, hangupResponse("this number is not accepting calls"))
}

// WriteErrorResponse implements ivr.Service. Without text to speech there's no way to say what happened.
func (s *service) WriteErrorResponse(w http.ResponseWriter, err error) error {
	return s.writeResponse(w, hangupResponse(err.Error()))
}

// WriteEmptyResponse implements ivr.Service
func (s *service) WriteEmptyResponse(w http.ResponseWriter, msg string) error {
	return s.writeResponse(w, hangupResponse(msg))
}

func hangupResponse(msg string) *Response {
	return &Response{Actions: &Action{Hangup: &Hangup{}}, Message: msg}
}

func (s *service) writeResponse(w http.ResponseWriter, resp *Response) error {
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write(jsonx.MustMarshal(resp))
	return err
}

// ActionsForSprint converts the events of a sprint into the actions to send, in order. There is always at least
// one, and only the last isn't a playback.
func ActionsForSprint(cfg *runtime.Config, es []events.Event) ([]*Action, error) {
	actions := make([]*Action, 0, len(es))
	ended := false

	// takes the last playback, if there is one, to become the prompt of a wait
	prompt := func() string {
		if len(actions) == 0 {
			return ""
		}
		url := actions[len(actions)-1].Playback.URL
		actions = actions[:len(actions)-1]
		return url
	}

	for _, e := range es {
		if ended {
			break
		}

		switch event := e.(type) {
		case *events.IVRCreated:
			if len(event.Msg.Attachments()) == 0 {
				// we can only play audio so there's nothing we can do with a message that is only text
				slog.Error("skipping IVR message without audio as channel has no text to speech", "text", event.Msg.Text())
				continue
			}
			for _, a := range event.Msg.Attachments() {
				a = models.NormalizeAttachment(cfg, a)
				actions = append(actions, &Action{Playback: &Playback{URL: a.URL()}})
			}

		case *events.MsgWait:
			ended = true

			switch hint := event.Hint.(type) {
			case *hints.Digits:
				url := prompt()
				digits := &GetDigits{
					URL:         url,
					SkipAudio:   url == "",
					NumDigits:   maxGatherDigits,
					Timeout:     gatherTimeout * 1000,
					FinishOnKey: hint.TerminatedBy,
				}
				if hint.Count != nil {
					digits.NumDigits = *hint.Count
				} else if digits.FinishOnKey == "" {
					// without a count or a key to end on, the caller would have to wait out the timeout
					digits.FinishOnKey = defaultFinishKey
				}
				actions = append(actions, &Action{GetDigits: digits})

			case *hints.Audio:
				record := &Record{URL: prompt(), MaxDuration: recordTimeout, PlayBeep: true}
				actions = append(actions, &Action{Record: record})

			default:
				return nil, fmt.Errorf("unable to use hint in IVR call, unknown type: %s", event.Hint.Type())
			}

		case *events.DialWait:
			// calls can only be forwarded to a number configured on the provider side, not one chosen by a flow
			slog.Error("ending IVR call as channel doesn't support dialing", "urn", event.URN)
			actions = append(actions, &Action{Hangup: &Hangup{}})
			ended = true
		}
	}

	if !ended {
		// no wait? call is over, hang up
		actions = append(actions, &Action{Hangup: &Hangup{}})
	}

	return actions, nil
}

func queueKey(callUUID core.CallUUID) string {
	return fmt.Sprintf("mnotify:actions:%s", callUUID)
}

func isTrue(v string) bool {
	return strings.EqualFold(v, "true") || v == "1"
}

// sameNumber checks whether two phone numbers are the same ignoring formatting and any country code or trunk prefix
func sameNumber(a, b string) bool {
	a, b = significantDigits(a), significantDigits(b)
	if len(a) < 7 || len(b) < 7 {
		return a == b
	}
	return strings.HasSuffix(a, b) || strings.HasSuffix(b, a)
}

func significantDigits(n string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, n)
	return strings.TrimLeft(digits, "0")
}
