package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/ai"
	"github.com/nyaruka/mailroom/core/models"
	"google.golang.org/genai"
)

const (
	TypeGoogle = "google"

	configAPIKey = "api_key"
)

func init() {
	models.RegisterLLMService(TypeGoogle, New)
}

// an LLM service implementation for Google GenAI
type service struct {
	client *genai.Client
	model  string
}

func New(m *models.LLM, c *http.Client) (flows.LLMService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: c,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating LLM client: %w", err)
	}

	return &service{client: client, model: m.Model()}, nil
}

func (s *service) Response(ctx context.Context, instructions, input string, maxTokens int) (*flows.LLMResponse, error) {
	config := &genai.GenerateContentConfig{
		Temperature:       genai.Ptr(float32(0.000001)),
		MaxOutputTokens:   int32(maxTokens),
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: instructions}}}}

	resp, err := s.client.Models.GenerateContent(ctx, s.model, genai.Text(input), config)
	if err != nil {
		return nil, s.error(err, instructions, input)
	}

	return &flows.LLMResponse{
		Output:     strings.TrimSpace(resp.Text()),
		TokensUsed: int64(resp.UsageMetadata.TotalTokenCount),
	}, nil
}

func (s *service) error(err error, instructions, input string) error {
	code := ai.ErrorUnknown
	var aerr *genai.APIError
	if errors.As(err, &aerr) {
		if aerr.Code == http.StatusUnauthorized {
			code = ai.ErrorCredentials
		} else if aerr.Code == http.StatusTooManyRequests {
			code = ai.ErrorRateLimit
		}
	}
	return &ai.ServiceError{Message: err.Error(), Code: code, Instructions: instructions, Input: input}
}
