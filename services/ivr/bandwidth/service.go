package bandwidth

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/routers/waits/hints"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	bandwidthChannelType = models.ChannelType("BW")

	usernameConfig           = "username"
	passwordConfig           = "password"
	accountIDConfig          = "account_id"
	VoiceApplicationIDConfig = "voice_application_id"

	gatherTimeout = 30
	recordTimeout = 600

	callPath   = `/accounts/{accountId}/calls`
	hangupPath = `/accounts/{accountId}/calls/{callId}`
)

var supportedSayLanguages = i18n.NewBCP47Matcher(
	"arb",
	"cmn-CN",
	"da-DK",
	"nl-NL",
	"en-AU",
	"en-GB",
	"en-IN",
	"en-US",
	"fr-FR",
	"fr-CA",
	"hi-IN",
	"de-DE",
	"is-IS",
	"it-IT",
	"ja-JP",
	"ko-KR",
	"nb-NO",
	"pl-PL",
	"pt-BR",
	"pt-PT",
	"ro-RO",
	"ru-RU",
	"es-ES",
	"es-MX",
	"es-US",
	"sv-SE",
	"tr-TR",
	"cy-GB",
)

type service struct {
	httpClient         *http.Client
	channel            *models.Channel
	username           string
	password           string
	accountID          string
	VoiceApplicationID string
}

func init() {
	ivr.RegisterService(bandwidthChannelType, NewServiceFromChannel)
}

// NewServiceFromChannel creates a new Bandwidth IVR service for the passed in username, password and accountID
func NewServiceFromChannel(httpClient *http.Client, channel *models.Channel) (ivr.Service, error) {
	username := channel.Config().GetString(usernameConfig, "")
	password := channel.Config().GetString(passwordConfig, "")
	accountId := channel.Config().GetString(accountIDConfig, "")
	applicationID := channel.Config().GetString(VoiceApplicationIDConfig, "")
	if username == "" || password == "" || accountId == "" || applicationID == "" {
		return nil, fmt.Errorf("missing username, password or account_id on channel config: %v for channel: %s", channel.Config(), channel.UUID())
	}

	return &service{
		httpClient:         httpClient,
		channel:            channel,
		username:           username,
		password:           password,
		accountID:          accountId,
		VoiceApplicationID: applicationID,
	}, nil
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, nil
	}
	r.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}

// CallIDForRequest implements ivr.Service.
func (s *service) CallIDForRequest(r *http.Request) (string, error) {
	body, err := readBody(r)
	if err != nil {
		return "", fmt.Errorf("error reading body from request: %w", err)
	}
	callID, err := jsonparser.GetString(body, "callId")
	if err != nil {
		return "", fmt.Errorf("invalid json body")
	}

	if callID == "" {
		return "", fmt.Errorf("no callId set on call")
	}
	return callID, nil
}

// CheckStartRequest implements ivr.Service.
func (s *service) CheckStartRequest(r *http.Request) models.CallError {
	return ""
}

// DownloadMedia implements ivr.Service.
func (s *service) DownloadMedia(url string) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.SetBasicAuth(s.username, s.password)
	return http.DefaultClient.Do(req)
}

// HangupCall implements ivr.Service.
func (s *service) HangupCall(callID string) (*httpx.Trace, error) {
	sendURL := BaseURL + strings.Replace(hangupPath, "{accountId}", s.accountID, -1)
	sendURL = strings.Replace(sendURL, "{callId}", callID, -1)

	hangupBody := map[string]string{"state": "completed"}

	trace, err := s.makeRequest(http.MethodPost, sendURL, hangupBody)
	if err != nil {
		return trace, fmt.Errorf("error trying to hangup call: %w", err)
	}

	if trace.Response.StatusCode != 200 {
		return trace, fmt.Errorf("received non 200 trying to hang up call: %d", trace.Response.StatusCode)
	}

	return trace, nil
}

// PreprocessResume implements ivr.Service.
func (s *service) PreprocessResume(ctx context.Context, rt *runtime.Runtime, call *models.Call, r *http.Request) ([]byte, error) {
	return nil, nil
}

// PreprocessStatus implements ivr.Service.
func (s *service) PreprocessStatus(ctx context.Context, rt *runtime.Runtime, r *http.Request) ([]byte, error) {
	return nil, nil
}

// RedactValues implements ivr.Service.
func (s *service) RedactValues(ch *models.Channel) []string {
	return []string{
		httpx.BasicAuth(ch.Config().GetString(usernameConfig, ""), ch.Config().GetString(passwordConfig, "")),
		ch.Config().GetString(passwordConfig, ""),
	}
}

// RequestCall implements ivr.Service.
func (s *service) RequestCall(number urns.URN, handleURL string, statusURL string, machineDetection bool) (ivr.CallID, *httpx.Trace, error) {
	sendURL := BaseURL + strings.Replace(callPath, "{accountId}", s.accountID, -1)

	callR := &CallRequest{
		To:            number.Path(),
		From:          s.channel.Address(),
		AnswerURL:     handleURL,
		DisconnectURL: statusURL,
		ApplicationID: s.VoiceApplicationID,
	}

	if machineDetection {
		callR.MachineDetection = &struct {
			Mode        string "json:\"mode\""
			CallbackURL string "json:\"callbackUrl\""
		}{Mode: "async", CallbackURL: statusURL} // if an answering machine answers, just hangup
	}
	trace, err := s.makeRequest(http.MethodPost, sendURL, callR)
	if err != nil {
		return ivr.NilCallID, trace, fmt.Errorf("error trying to start call: %w", err)
	}

	if trace.Response.StatusCode != http.StatusCreated {
		return ivr.NilCallID, trace, fmt.Errorf("received non 201 status for call start: %d", trace.Response.StatusCode)
	}

	// parse out our call sid
	call := &CallResponse{}
	err = json.Unmarshal(trace.ResponseBody, call)
	if err != nil || call.CallID == "" {
		return ivr.NilCallID, trace, fmt.Errorf("unable to read call with uuid")
	}

	slog.Debug("requested call", "body", string(trace.ResponseBody), "status", trace.Response.StatusCode)

	return ivr.CallID(call.CallID), trace, nil

}

type ResumeRequest struct {
	EventType        string    `json:"eventType"`
	EventTime        time.Time `json:"eventTime"`
	AccountID        string    `json:"accountId"`
	ApplicationID    string    `json:"applicationId"`
	To               string    `json:"to"`
	From             string    `json:"from"`
	Direction        string    `json:"direction"`
	CallID           string    `json:"callID"`
	StartTime        time.Time `json:"startTime"`
	Digits           string    `json:"digits"`
	TerminatingDigit string    `json:"terminatingDigit"`
	MediaURL         string    `json:"mediaUrl"`
	Status           string    `json:"status"`
	FileFormat       string    `json:"fileFormat"`
}

// ResumeForRequest implements ivr.Service.
func (s *service) ResumeForRequest(r *http.Request) (ivr.Resume, error) {

	// this could be a timeout, in which case we return an empty input
	timeout := r.Form.Get("timeout")
	if timeout == "true" {
		return ivr.InputResume{}, nil
	}

	// this could be empty, in which case we return an empty input
	empty := r.Form.Get("empty")
	if empty == "true" {
		return ivr.InputResume{}, nil
	}

	// otherwise grab the right field based on our wait type
	waitType := r.Form.Get("wait_type")

	// parse our input
	input := &ResumeRequest{}
	bb, err := readBody(r)
	if err != nil {
		return nil, fmt.Errorf("error reading request body: %w", err)
	}

	err = json.Unmarshal(bb, input)
	if err != nil {
		return nil, fmt.Errorf("unable to parse request body: %w", err)
	}

	switch waitType {

	case "gather":
		return ivr.InputResume{Input: input.Digits}, nil

	case "record":
		recordingURL := input.MediaURL
		recordingStatus := input.Status

		if recordingURL == "" || recordingStatus == "" {
			return ivr.InputResume{}, nil
		}
		slog.Info("input found recording", "recording_url", recordingURL)
		return ivr.InputResume{Attachment: utils.Attachment("audio:" + recordingURL)}, nil

	case "dial":
		duration := time.Now().Sub(input.StartTime).Seconds()

		return ivr.DialResume{Status: flows.DialStatus("answered"), Duration: int(duration)}, nil

	default:
		return nil, fmt.Errorf("unknown wait_type: %s", waitType)
	}

}

type StatusRequest struct {
	EventType     string    `json:"eventType"`
	EventTime     time.Time `json:"eventTime"`
	AccountID     string    `json:"accountId"`
	ApplicationID string    `json:"applicationId"`
	To            string    `json:"to"`
	From          string    `json:"from"`
	Direction     string    `json:"direction"`
	CallID        string    `json:"callID"`
	StartTime     time.Time `json:"startTime"`
	EndTime       time.Time `json:"endTime"`
	Cause         string    `json:"cause"`

	MachineDetectionResult *struct {
		Value string `json:"value"`
	} `json:"machineDetectionResult"`
}

// StatusForRequest implements ivr.Service.
func (s *service) StatusForRequest(r *http.Request) (models.CallStatus, models.CallError, int) {
	body, err := readBody(r)
	if err != nil {
		slog.Error("error reading status request body", "error", err)
		return models.CallStatusErrored, models.CallErrorProvider, 0
	}

	status := &StatusRequest{}
	err = json.Unmarshal(body, status)
	if err != nil {
		slog.Error("error unmarshalling status request body", "error", err, "body", string(body))
		return models.CallStatusErrored, models.CallErrorProvider, 0
	}

	if status.EventType == "machineDetectionComplete" {
		if status.MachineDetectionResult.Value != "human" {
			return models.CallStatusErrored, models.CallErrorMachine, 0
		}
	}

	if status.EventType == "disconnect" || status.EventType == "transferDisconnect" {
		switch status.Cause {
		case "hangup":
			duration := status.EndTime.Sub(status.StartTime).Seconds()
			return models.CallStatusCompleted, "", int(duration)
		case "busy":
			return models.CallStatusErrored, models.CallErrorBusy, 0
		case "timeout", "rejected", "cancel":
			return models.CallStatusErrored, models.CallErrorNoAnswer, 0

		case "unknown", "error", "application-error", "invalid-bxml", "callback-error":
			return models.CallStatusErrored, models.CallErrorProvider, 0

		case "account-limit", "node-capacity-exceeded":
			return models.CallStatusErrored, models.CallErrorProvider, 0

		default:
			slog.Error("unknown call disconnect cause in status callback", "cause", status.Cause)
			return models.CallStatusFailed, models.CallErrorProvider, 0
		}

	}

	return models.CallStatusInProgress, "", 0
}

// URNForRequest implements ivr.Service.
func (s *service) URNForRequest(r *http.Request) (urns.URN, error) {
	body, err := readBody(r)
	if err != nil {
		return "", fmt.Errorf("error reading body from request: %w", err)
	}
	tel, err := jsonparser.GetString(body, "to")
	if tel == "" {
		return "", errors.New("no 'to' key found in request")
	}
	return urns.ParsePhone(tel, "", true, false)
}

// ValidateRequestSignature implements ivr.Service.
func (s *service) ValidateRequestSignature(r *http.Request) error {
	return nil
}

// WriteEmptyResponse implements ivr.Service.
func (s *service) WriteEmptyResponse(w http.ResponseWriter, msg string) error {
	return s.writeResponse(w, &Response{
		Message: strings.Replace(msg, "--", "__", -1),
	})
}

// WriteErrorResponse implements ivr.Service.
func (s *service) WriteErrorResponse(w http.ResponseWriter, err error) error {
	return s.writeResponse(w, &Response{
		Message: strings.Replace(err.Error(), "--", "__", -1),
		Commands: []any{
			SpeakSentence{Text: ivr.ErrorMessage},
			Hangup{},
		},
	})
}

// WriteRejectResponse implements ivr.Service.
func (s *service) WriteRejectResponse(w http.ResponseWriter) error {
	return s.writeResponse(w, &Response{
		Message: strings.Replace("", "--", "__", -1),
		Commands: []any{
			SpeakSentence{Text: "This number is not accepting calls"},
			Hangup{},
		},
	})
}

// WriteSessionResponse implements ivr.Service.
func (s *service) WriteSessionResponse(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.Channel, scene *runner.Scene, number urns.URN, resumeURL string, req *http.Request, w http.ResponseWriter) error {
	// for errored sessions we should just output our error body
	if scene.Session().Status() == flows.SessionStatusFailed {
		return fmt.Errorf("cannot write IVR response for failed session")
	}

	// get our response
	response, err := ResponseForSprint(rt, oa.Env(), number, resumeURL, scene.Sprint().Events(), true)
	if err != nil {
		return fmt.Errorf("unable to build response for IVR call: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write([]byte(response))
	if err != nil {
		return fmt.Errorf("error writing IVR response: %w", err)
	}

	return nil
}

func (s *service) writeResponse(w http.ResponseWriter, resp *Response) error {
	marshalled, err := xml.Marshal(resp)
	if err != nil {
		return err
	}
	w.Write([]byte(xml.Header))
	_, err = w.Write(marshalled)
	return err
}

func (s *service) makeRequest(method string, sendURL string, body any) (*httpx.Trace, error) {
	bb := jsonx.MustMarshal(body)
	req, _ := http.NewRequest(method, sendURL, bytes.NewReader(bb))
	req.SetBasicAuth(s.username, s.password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	return httpx.DoTrace(s.httpClient, req, nil, nil, -1)
}

func ResponseForSprint(rt *runtime.Runtime, env envs.Environment, urn urns.URN, resumeURL string, es []flows.Event, indent bool) (string, error) {
	r := &Response{}
	commands := make([]any, 0)
	hasWait := false

	for _, e := range es {
		switch event := e.(type) {
		case *events.IVRCreatedEvent:
			if len(event.Msg.Attachments()) == 0 {
				var locales []i18n.Locale
				if event.Msg.Locale() != "" {
					locales = append(locales, event.Msg.Locale())
				}
				locales = append(locales, env.DefaultLocale())
				lang := supportedSayLanguages.ForLocales(locales...)
				lang = strings.Replace(lang, "-", "_", -1)

				commands = append(commands, &SpeakSentence{Text: event.Msg.Text(), Locale: lang})
			} else {
				for _, a := range event.Msg.Attachments() {
					a = models.NormalizeAttachment(rt.Config, a)
					commands = append(commands, PlayAudio{URL: a.URL()})
				}
			}

		case *events.MsgWaitEvent:
			hasWait = true
			switch hint := event.Hint.(type) {
			case *hints.DigitsHint:
				resumeURL = resumeURL + "&wait_type=gather"
				gather := &Gather{
					URL:      resumeURL,
					Commands: commands,
					Timeout:  gatherTimeout,
				}
				if hint.Count != nil {
					gather.MaxDigits = *hint.Count
				}
				gather.TerminatingDigits = hint.TerminatedBy
				r.Gather = gather
				r.Commands = append(r.Commands, Redirect{URL: resumeURL + "&timeout=true"})

			case *hints.AudioHint:
				resumeURL = resumeURL + "&wait_type=record"
				commands = append(commands, Record{URL: resumeURL, MaxDuration: recordTimeout})
				commands = append(commands, Redirect{URL: resumeURL + "&empty=true"})
				r.Commands = commands

			default:
				return "", fmt.Errorf("unable to use hint in IVR call, unknown type: %s", event.Hint.Type())
			}

		case *events.DialWaitEvent:
			hasWait = true
			phoneNumbers := make([]PhoneNumber, 0)
			phoneNumbers = append(phoneNumbers, PhoneNumber{Number: event.URN.Path()})
			transfer := Transfer{URL: resumeURL + "&wait_type=dial", PhoneNumbers: phoneNumbers, Timeout: event.DialLimitSeconds, TimeLimit: event.CallLimitSeconds}
			commands = append(commands, transfer)
			r.Commands = commands
		}
	}

	if !hasWait {
		// no wait? call is over, hang up
		commands = append(commands, Hangup{})
		r.Commands = commands
	}

	var body []byte
	var err error
	if indent {
		body, err = xml.MarshalIndent(r, "", "  ")
	} else {
		body, err = xml.Marshal(r)
	}
	if err != nil {
		return "", fmt.Errorf("unable to marshal twiml body: %w", err)
	}

	return xml.Header + string(body), nil
}
