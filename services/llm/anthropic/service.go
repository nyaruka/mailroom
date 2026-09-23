package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const (
	TypeAnthropic = "anthropic"

	configAPIKey = "api_key"
)

// models which think by default but accept thinking being disabled. Later models don't allow disabling thinking
// at all and earlier models don't think unless asked to.
var thinksByDefault = map[string]bool{
	"claude-opus-5":   true,
	"claude-sonnet-5": true,
}

func init() {
	models.RegisterLLMService(TypeAnthropic, New)
}

// an LLM service implementation for Anthropic
type service struct {
	client anthropic.Client
	model  string
}

func New(rt *runtime.Runtime, m *models.LLM, c *http.Client) (flows.ModelService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	return &service{
		client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithHTTPClient(c)),
		model:  m.Model(),
	}, nil
}

func (s *service) Response(ctx context.Context, instructions, input string, maxTokens int) (*core.ModelResponse, error) {
	params := anthropic.MessageNewParams{
		Model:  anthropic.Model(s.model),
		System: []anthropic.TextBlockParam{{Text: instructions}},
		Messages: []anthropic.MessageParam{
			{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					{
						OfText: &anthropic.TextBlockParam{Text: input},
					},
				},
			},
		},
		MaxTokens: int64(maxTokens),
	}

	// thinking adds latency and its tokens count against MaxTokens, which can truncate what are short responses
	if thinksByDefault[s.model] {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
	}

	resp, err := s.client.Messages.New(ctx, params)
	if err != nil {
		return nil, s.error(err, instructions, input)
	}

	var output strings.Builder
	for _, content := range resp.Content {
		if content.Type == "text" {
			output.WriteString(content.Text)
		}
	}

	return &core.ModelResponse{
		Output:       s.cleanOutput(output.String()),
		TokensInput:  resp.Usage.InputTokens,
		TokensOutput: resp.Usage.OutputTokens,
	}, nil
}

// Classify prompts the model, and as the API doesn't provide logprobs, the confidence is approximated.
func (s *service) Classify(ctx context.Context, input string, options []*core.ClassifierOption) (*core.Classification, error) {
	resp, err := s.Response(ctx, ai.ClassifyInstructions(options), input, ai.ClassifyMaxTokens)
	if err != nil {
		return nil, err
	}

	return ai.NewClassification(resp, nil, options)
}

func (s *service) error(err error, instructions, input string) error {
	code := ai.ErrorUnknown
	if aerr, ok := errors.AsType[*anthropic.Error](err); ok {
		switch aerr.StatusCode {
		case http.StatusUnauthorized:
			code = ai.ErrorCredentials
		case http.StatusTooManyRequests:
			code = ai.ErrorRateLimit
		}
	}
	return &ai.ServiceError{Message: err.Error(), Code: code, Instructions: instructions, Input: input}
}

func (s *service) cleanOutput(output string) string {
	output = strings.ReplaceAll(output, "<<ASSISTANT_CONVERSATION_START>>", "")
	output = strings.ReplaceAll(output, "<<ASSISTANT_CONVERSATION_END>>", "")
	return strings.TrimSpace(output)
}
