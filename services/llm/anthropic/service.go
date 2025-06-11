package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/ai"
	"github.com/nyaruka/mailroom/core/models"
)

const (
	TypeAnthropic = "anthropic"

	configAPIKey = "api_key"
)

func init() {
	models.RegisterLLMService(TypeAnthropic, New)
}

// an LLM service implementation for Anthropic
type service struct {
	client anthropic.Client
	model  string
}

func New(m *models.LLM, c *http.Client) (flows.LLMService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	return &service{
		client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithHTTPClient(c)),
		model:  m.Model(),
	}, nil
}

func (s *service) Response(ctx context.Context, instructions, input string, maxTokens int) (*flows.LLMResponse, error) {
	resp, err := s.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model: anthropic.Model(s.model),
		Messages: []anthropic.MessageParam{
			{
				Role: anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{
					{
						OfText: &anthropic.TextBlockParam{Text: instructions},
					},
				},
			},
			{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					{
						OfText: &anthropic.TextBlockParam{Text: input},
					},
				},
			},
		},
		Temperature: anthropic.Float(0.000001),
		MaxTokens:   2500,
	})
	if err != nil {
		return nil, s.error(err, instructions, input)
	}

	var output strings.Builder
	for _, content := range resp.Content {
		if content.Type == "text" {
			output.WriteString(s.cleanOutput(content.Text))
		}
	}

	return &flows.LLMResponse{Output: output.String(), TokensUsed: resp.Usage.InputTokens + resp.Usage.OutputTokens}, nil
}

func (s *service) error(err error, instructions, input string) error {
	code := ai.ErrorUnknown
	var aerr *anthropic.Error
	if errors.As(err, &aerr) {
		if aerr.StatusCode == http.StatusUnauthorized {
			code = ai.ErrorCredentials
		} else if aerr.StatusCode == http.StatusTooManyRequests {
			code = ai.ErrorRateLimit
		}
	}
	return &ai.ServiceError{Message: err.Error(), Code: code, Instructions: instructions, Input: input}
}

func (s *service) cleanOutput(output string) string {
	output = strings.Replace(output, "<<ASSISTANT_CONVERSATION_START>>", "", -1)
	output = strings.Replace(output, "<<ASSISTANT_CONVERSATION_END>>", "", -1)
	return strings.TrimSpace(output)
}
