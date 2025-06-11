package deepseek

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/ai"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

const (
	TypeDeepSeek = "deepseek"

	configAPIKey = "api_key"
)

func init() {
	models.RegisterLLMService(TypeDeepSeek, New)
}

// an LLM service implementation for DeepSeek
type service struct {
	client openai.Client
	model  string
}

func New(m *models.LLM, c *http.Client) (flows.LLMService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	return &service{
		client: openai.NewClient(option.WithBaseURL("https://api.deepseek.com"), option.WithAPIKey(apiKey), option.WithHTTPClient(c)),
		model:  m.Model(),
	}, nil
}

func (s *service) Response(ctx context.Context, instructions, input string, maxTokens int) (*flows.LLMResponse, error) {
	resp, err := s.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel(s.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(instructions),
			openai.UserMessage(input),
		},
		Temperature: openai.Float(0.000001),
		MaxTokens:   openai.Int(int64(maxTokens)),
	})
	if err != nil {
		return nil, s.error(err, instructions, input)
	}

	return &flows.LLMResponse{
		Output:     strings.TrimSpace(resp.Choices[0].Message.Content),
		TokensUsed: resp.Usage.TotalTokens,
	}, nil
}

func (s *service) error(err error, instructions, input string) error {
	code := ai.ErrorUnknown
	var aerr *responses.Error
	if errors.As(err, &aerr) {
		if aerr.StatusCode == http.StatusUnauthorized {
			code = ai.ErrorCredentials
		} else if aerr.StatusCode == http.StatusTooManyRequests {
			code = ai.ErrorRateLimit
		}
	}
	return &ai.ServiceError{Message: err.Error(), Code: code, Instructions: instructions, Input: input}
}
