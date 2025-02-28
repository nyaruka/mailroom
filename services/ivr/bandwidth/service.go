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

	"github.com/buger/jsonparser"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/routers/waits/hints"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	bandwidthChannelType = models.ChannelType("BW")

	usernameConfig     = "username"
	passwordConfig     = "password"
	accountIDConfig    = "account_id"
	aplicationIDConfig = "application_id"

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
	httpClient    *http.Client
	channel       *models.Channel
	username      string
	password      string
	accountID     string
	applicationID string
}

func init() {
	ivr.RegisterServiceType(bandwidthChannelType, NewServiceFromChannel)
}

// NewServiceFromChannel creates a new Bandwidth IVR service for the passed in username, password and accountID
func NewServiceFromChannel(httpClient *http.Client, channel *models.Channel) (ivr.Service, error) {
	username := channel.ConfigValue(usernameConfig, "")
	password := channel.ConfigValue(passwordConfig, "")
	accountId := channel.ConfigValue(accountIDConfig, "")
	aplicationID := channel.ConfigValue(aplicationIDConfig, "")
	if username == "" || password == "" || accountId == "" || aplicationID == "" {
		return nil, fmt.Errorf("missing username, password or account_id on channel config: %v for channel: %s", channel.Config(), channel.UUID())
	}

	return &service{
		httpClient:    httpClient,
		channel:       channel,
		username:      username,
		password:      password,
		accountID:     accountId,
		applicationID: aplicationID,
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
	body, err := readBody(r)
	if err != nil {
		return ""
	}
	machineDectection, err := jsonparser.GetString(body, "machineDetectionResult", "value")
	if machineDectection != "human" {
		return models.CallErrorMachine
	}
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
		httpx.BasicAuth(ch.ConfigValue(usernameConfig, ""), ch.ConfigValue(passwordConfig, "")),
		ch.ConfigValue(passwordConfig, ""),
	}
}

// RequestCall implements ivr.Service.
func (s *service) RequestCall(number urns.URN, handleURL string, statusURL string, machineDetection bool) (ivr.CallID, *httpx.Trace, error) {
	sendURL := BaseURL + strings.Replace(callPath, "{accountId}", s.accountID, -1)

	callR := &CallRequest{
		To:            number.Path(),
		From:          s.channel.Address(),
		AnswerURL:     handleURL,
		ApplicationID: s.applicationID,
	}

	if machineDetection {
		callR.MachineDetection = &struct {
			Mode string "json:\"mode\""
		}{Mode: "sync"} // if an answering machine answers, just hangup
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
		return ivr.NilCallID, trace, fmt.Errorf("unable to read call uuid")
	}

	slog.Debug("requested call", "body", string(trace.ResponseBody), "status", trace.Response.StatusCode)

	return ivr.CallID(call.CallID), trace, nil

}

// ResumeForRequest implements ivr.Service.
func (s *service) ResumeForRequest(r *http.Request) (ivr.Resume, error) {
	panic("unimplemented")
}

// StatusForRequest implements ivr.Service.
func (s *service) StatusForRequest(r *http.Request) (models.CallStatus, models.CallError, int) {
	panic("unimplemented")
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
func (s *service) WriteSessionResponse(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.Channel, call *models.Call, session *models.Session, number urns.URN, resumeURL string, req *http.Request, w http.ResponseWriter) error {
	// for errored sessions we should just output our error body
	if session.Status() == models.SessionStatusFailed {
		return fmt.Errorf("cannot write IVR response for failed session")
	}

	// otherwise look for any say events
	sprint := session.Sprint()
	if sprint == nil {
		return fmt.Errorf("cannot write IVR response for session with no sprint")
	}

	// get our response
	response, err := ResponseForSprint(rt, oa.Env(), number, resumeURL, sprint.Events(), true)
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
	req.SetBasicAuth(s.accountID, s.password)
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
