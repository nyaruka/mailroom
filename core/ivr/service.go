package ivr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// ServiceConstructor defines our signature for creating a new IVR service from a channel
type ServiceConstructor func(*http.Client, *models.Channel) (Service, error)

var registeredTypes = make(map[models.ChannelType]ServiceConstructor)

// RegisterService registers a new IVR service for the given channel type
func RegisterService(channelType models.ChannelType, constructor ServiceConstructor) {
	registeredTypes[channelType] = constructor
}

// GetService creates the right kind of IVR service for the passed in channel. The httpClient's transport is
// used as the base for the service's outbound provider calls, so tests can pass a client with a mocking
// transport while production passes the runtime's shared client.
func GetService(httpClient *http.Client, channel *models.Channel) (Service, error) {
	constructor := registeredTypes[channel.Type()]
	if constructor == nil {
		return nil, fmt.Errorf("no registered IVR service for channel type: %s", channel.Type())
	}

	return constructor(httpClient, channel)
}

// TraceRequest sends req through client and captures a single Trace of the request and response. It's the
// composable replacement for the deprecated httpx.DoTrace used by IVR service implementations: it wraps the
// client's transport in an httpx.TracesTransport for this one request and returns the resulting trace. On a
// transport-level failure the trace still carries the request (with no response) and the underlying error is
// returned with http.Client.Do's *url.Error wrapper removed, matching what DoTrace surfaced.
func TraceRequest(client *http.Client, req *http.Request) (*httpx.Trace, error) {
	tracer := httpx.WithTraces(client.Transport)
	traced := &http.Client{Transport: tracer, Timeout: client.Timeout}

	resp, err := traced.Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	var trace *httpx.Trace
	if traces := tracer.Traces(); len(traces) > 0 {
		trace = traces[len(traces)-1]
	}

	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
	}
	return trace, err
}

// Service defines the interface IVR services must satisfy
type Service interface {
	RequestCall(number urns.URN, handleURL string, statusURL string, machineDetection bool) (CallID, *httpx.Trace, error)

	HangupCall(externalID string) (*httpx.Trace, error)

	WriteSessionResponse(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.Channel, scene *runner.Scene, number urns.URN, resumeURL string, req *http.Request, w http.ResponseWriter) error
	WriteRejectResponse(w http.ResponseWriter) error
	WriteErrorResponse(w http.ResponseWriter, err error) error
	WriteEmptyResponse(w http.ResponseWriter, msg string) error

	ResumeForRequest(r *http.Request) (Resume, error)

	// StatusForRequest returns the call status for the passed in request, and if it's an error the reason,
	// and if available, the current call duration
	StatusForRequest(r *http.Request) (models.CallStatus, models.CallError, int)

	// CheckStartRequest checks the start request from the service is as we expect and if not returns an error reason
	CheckStartRequest(r *http.Request) models.CallError

	PreprocessResume(ctx context.Context, rt *runtime.Runtime, call *models.Call, r *http.Request) ([]byte, error)

	PreprocessStatus(ctx context.Context, rt *runtime.Runtime, r *http.Request) ([]byte, error)

	ValidateRequestSignature(r *http.Request) error

	DownloadMedia(url string) (*http.Response, error)

	URNForRequest(r *http.Request) (urns.URN, error)

	CallIDForRequest(r *http.Request) (string, error)

	RedactValues(*models.Channel) []string
}
