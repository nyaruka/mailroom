package ivr

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// ServiceConstructor defines our signature for creating a new IVR service from a channel
type ServiceConstructor func(*http.Client, *models.Channel) (Service, error)

var registeredTypes = make(map[models.ChannelType]ServiceConstructor)

// RegisterService registers a new IVR service for the given channel type
func RegisterService(channelType models.ChannelType, constructor ServiceConstructor) {
	registeredTypes[channelType] = constructor
}

// GetService creates the right kind of IVR service for the passed in channel
func GetService(channel *models.Channel) (Service, error) {
	constructor := registeredTypes[channel.Type()]
	if constructor == nil {
		return nil, fmt.Errorf("no registered IVR service for channel type: %s", channel.Type())
	}

	return constructor(http.DefaultClient, channel)
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
