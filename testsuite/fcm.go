package testsuite

import (
	"context"
	"errors"

	"firebase.google.com/go/v4/messaging"
	"github.com/nyaruka/goflow/utils"
)

type MockFCMService struct {
	tokens []string

	// log of messages sent to this endpoint
	Messages []*messaging.Message
}

func (m *MockFCMService) Client(ctx context.Context, androidFCMServiceAccountFile string) *MockFCMClient {
	return &MockFCMClient{FCMService: m}
}

func (m *MockFCMService) GetClient(ctx context.Context) *MockFCMClient {
	return m.Client(ctx, "testfiles/android.json")
}

func NewMockFCMService(tokens ...string) *MockFCMService {
	mock := &MockFCMService{tokens: tokens}
	return mock
}

type MockFCMClient struct {
	FCMService *MockFCMService
}

func (fc *MockFCMClient) Send(ctx context.Context, message *messaging.Message) (string, error) {
	var result string
	var err error
	fc.FCMService.Messages = append(fc.FCMService.Messages, message)
	if utils.StringSliceContains(fc.FCMService.tokens, message.Token, false) {
		result = "success"
	} else {
		result = "error"
		err = errors.New("401 error: 401 Unauthorized")
	}

	return result, err
}
