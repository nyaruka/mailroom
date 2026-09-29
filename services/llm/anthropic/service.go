package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/nyaruka/gocommon/i18n"
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

// how to turn off thinking for models which think by default. Thinking adds latency and its tokens count against
// MaxTokens, which can truncate what are short responses. Models not listed either don't think unless asked to or
// don't allow thinking to be turned off.
var thinkingOff = map[string]anthropic.ThinkingConfigParamUnion{
	"claude-opus-5":     {OfDisabled: &anthropic.ThinkingConfigDisabledParam{}},
	"claude-sonnet-5":   {OfDisabled: &anthropic.ThinkingConfigDisabledParam{}},
	"claude-sonnet-5-5": {OfBetweenTools: &anthropic.ThinkingConfigBetweenToolsParam{}},
}

func init() {
	models.RegisterLLMService(TypeAnthropic, New)
}

// an LLM service implementation for Anthropic
type service struct {
	client          anthropic.Client
	model           string
	maxOutputTokens int
}

func New(rt *runtime.Runtime, m *models.LLM, c *http.Client) (flows.ModelService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	return &service{
		client:          anthropic.NewClient(option.WithAPIKey(apiKey), option.WithHTTPClient(c)),
		model:           m.Model(),
		maxOutputTokens: m.MaxOutputTokens(),
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

	if thinking, ok := thinkingOff[s.model]; ok {
		params.Thinking = thinking
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
		Output: s.cleanOutput(output.String()),
		Tokens: core.ModelTokens{Input: resp.Usage.InputTokens, Output: resp.Usage.OutputTokens},
	}, nil
}

func (s *service) Classify(ctx context.Context, input string, options []*core.ClassifierOption) (*core.Classification, error) {
	return ai.ClassifyByPrompt(ctx, s, input, options)
}

func (s *service) Translate(ctx context.Context, source, target i18n.Language, items map[string][]string) (*core.Translation, error) {
	return ai.TranslateByPrompt(ctx, s, source, target, items, s.maxOutputTokens)
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
