package testsuite

import (
	"context"
	"net/http"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

var IVRService = &MockIVRService{}

func NewIVRServiceFactory(httpClient *http.Client, channel *models.Channel) (ivr.Service, error) {
	return IVRService, nil
}

type MockIVRService struct {
	CallID    ivr.CallID
	CallError error
}

func (s *MockIVRService) RequestCall(number urns.URN, handleURL string, statusURL string, machineDetection bool) (ivr.CallID, *httpx.Trace, error) {
	return s.CallID, nil, s.CallError
}

func (s *MockIVRService) HangupCall(externalID string) (*httpx.Trace, error) {
	return nil, nil
}

func (s *MockIVRService) WriteSessionResponse(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.Channel, scene *runner.Scene, number urns.URN, resumeURL string, req *http.Request, w http.ResponseWriter) error {
	return nil
}

func (s *MockIVRService) WriteRejectResponse(w http.ResponseWriter) error {
	return nil
}

func (s *MockIVRService) WriteErrorResponse(w http.ResponseWriter, err error) error {
	return nil
}

func (s *MockIVRService) WriteEmptyResponse(w http.ResponseWriter, msg string) error {
	return nil
}

func (s *MockIVRService) ResumeForRequest(r *http.Request) (ivr.Resume, error) {
	return nil, nil
}

func (s *MockIVRService) StatusForRequest(r *http.Request) (models.CallStatus, models.CallError, int) {
	return models.CallStatusFailed, models.CallErrorProvider, 10
}

func (s *MockIVRService) CheckStartRequest(r *http.Request) models.CallError {
	return ""
}

func (s *MockIVRService) PreprocessResume(ctx context.Context, rt *runtime.Runtime, call *models.Call, r *http.Request) ([]byte, error) {
	return nil, nil
}

func (s *MockIVRService) PreprocessStatus(ctx context.Context, rt *runtime.Runtime, r *http.Request) ([]byte, error) {
	return nil, nil
}

func (s *MockIVRService) ValidateRequestSignature(r *http.Request) error {
	return nil
}

func (s *MockIVRService) DownloadMedia(url string) (*http.Response, error) {
	return nil, nil
}

func (s *MockIVRService) URNForRequest(r *http.Request) (urns.URN, error) {
	return urns.NilURN, nil
}

func (s *MockIVRService) CallIDForRequest(r *http.Request) (string, error) {
	return "", nil
}

func (s *MockIVRService) RedactValues(*models.Channel) []string {
	return []string{"sesame"}
}
