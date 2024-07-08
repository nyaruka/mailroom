package testsuite

import (
	"context"
	"errors"
	"fmt"

	"firebase.google.com/go/v4/auth"
	"firebase.google.com/go/v4/messaging"
	"github.com/nyaruka/goflow/utils"
)

type MockFirebaseService struct {
	tokens []string

	// log of messages sent to this endpoint
	Messages []*messaging.Message
}

func (m *MockFirebaseService) FirebaseCloudMessagingClient(ctx context.Context, androidFCMServiceAccountFile string) *MockFirebaseCloudMessagingClient {
	return &MockFirebaseCloudMessagingClient{FirebaseService: m}
}

func (m *MockFirebaseService) GetFirebaseCloudMessagingClient(ctx context.Context) *MockFirebaseCloudMessagingClient {
	return m.FirebaseCloudMessagingClient(ctx, "testfiles/android.json")
}

func (m *MockFirebaseService) AuthClient(ctx context.Context, androidFCMServiceAccountFile string) *MockFirebaseAuthClient {
	return &MockFirebaseAuthClient{FirebaseService: m}
}

func (m *MockFirebaseService) GetAuthClient(ctx context.Context) *MockFirebaseAuthClient {
	return m.AuthClient(ctx, "testfiles/android.json")
}

func NewMockFirebaseService(tokens ...string) *MockFirebaseService {
	mock := &MockFirebaseService{tokens: tokens}
	return mock
}

type MockFirebaseAuthClient struct {
	*auth.Client
	FirebaseService *MockFirebaseService
}

func (c *MockFirebaseAuthClient) VerifyIDToken(ctx context.Context, idToken string) (*auth.Token, error) {
	if utils.StringSliceContains(c.FirebaseService.tokens, idToken, false) {
		return &auth.Token{}, nil
	} else {
		return nil, fmt.Errorf("invalid token")
	}
}

type MockFirebaseCloudMessagingClient struct {
	*messaging.Client
	FirebaseService *MockFirebaseService
}

func (fc *MockFirebaseCloudMessagingClient) Send(ctx context.Context, message *messaging.Message) (string, error) {
	var result string
	var err error
	fc.FirebaseService.Messages = append(fc.FirebaseService.Messages, message)
	if utils.StringSliceContains(fc.FirebaseService.tokens, message.Token, false) {
		result = "success"
	} else {
		result = "error"
		err = errors.New("401 error: 401 Unauthorized")
	}

	return result, err
}
